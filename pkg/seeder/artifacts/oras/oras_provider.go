// Copyright 2023 Hedgehog
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oras

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"go.githedgehog.com/dasboot/pkg/log"
	"go.githedgehog.com/dasboot/pkg/seeder/artifacts"
	"go.uber.org/zap"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

type orasProvider struct {
	ctx context.Context

	serverCAPath      string
	clientCertPath    string
	clientKeyPath     string
	username          string
	password          string
	accessToken       string
	refreshToken      string
	fileStoreBasePath string

	url      *url.URL
	registry *remote.Registry
}

var _ artifacts.Provider = &orasProvider{}

func Provider(ctx context.Context, registryURL, fileStoreBasePath string, options ...ProviderOption) (artifacts.Provider, error) {
	var err error
	// apply options
	ret := &orasProvider{
		ctx:               ctx,
		fileStoreBasePath: fileStoreBasePath,
	}
	for _, opt := range options {
		opt(ret)
	}

	// create file store
	if fileStoreBasePath == "" {
		return nil, fmt.Errorf("fileStoreBasePath must not be empty")
	}

	// parse URL
	ret.url, err = url.Parse(registryURL)
	if err != nil {
		return nil, fmt.Errorf("parsing registry URL: %w", err)
	}
	if ret.url.Scheme != "oci" {
		return nil, fmt.Errorf("registry URL must have OCI scheme, got '%s'", ret.url.Scheme)
	}

	ret.registry, err = remote.NewRegistry(ret.url.Host)
	if err != nil {
		return nil, fmt.Errorf("create ORAS client: %w", err)
	}

	creds := func(_ context.Context, target string) (auth.Credential, error) {
		if ret.username != "" || ret.password != "" || ret.accessToken != "" || ret.refreshToken != "" {
			if target == ret.url.Host {
				return auth.Credential{
					Username:     ret.username,
					Password:     ret.password,
					AccessToken:  ret.accessToken,
					RefreshToken: ret.refreshToken,
				}, nil
			}
		}
		return auth.EmptyCredential, nil
	}

	ret.registry.Client = &auth.Client{
		Credential: creds,
		Cache:      auth.NewCache(),
		Client: &http.Client{
			Transport: &http.Transport{
				// take proxy from the environment if set
				Proxy: http.ProxyFromEnvironment,

				// There are no connection timeouts
				// so we are doing pretty much exactly what
				// Go is doing itself
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
					// increasing this from the default Go settings
					// as we can ensure that if there is IPv6 in our network
					// it actually *must* be configured correctly.
					FallbackDelay: 600 * time.Millisecond,
				}).DialContext,

				// These are HTTP keep alives (not TCP keepalives)
				// and their corresponding idle connection settings and timeouts
				DisableKeepAlives: false,
				MaxIdleConns:      10,
				MaxConnsPerHost:   3,
				IdleConnTimeout:   90 * time.Second,

				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,

				// as we are setting our own DialContext and TLSClientConfig
				// Go internally disables trying to use HTTP/2 (why?)
				// so we are reenabling this here
				ForceAttemptHTTP2: true,

				// Our TLS configuration that we prepped before
				TLSClientConfig: &tls.Config{
					Rand:         rand.Reader,
					Time:         time.Now,
					RootCAs:      caPool(ret.serverCAPath),
					Certificates: clientCertificates(ret.clientCertPath, ret.clientKeyPath),
					MinVersion:   tls.VersionTLS12,
				},
			},
		},
	}

	return ret, nil
}

// Get implements artifacts.Provider
func (op *orasProvider) Get(artifact string) (rc io.ReadCloser) {
	ctx, cancel := context.WithTimeout(op.ctx, time.Second*60)
	defer cancel()

	// build repo name from artifact
	// we need to remove the left most '/' as it would render an invalid repository name
	repoName := path.Join(op.url.Path, artifact)
	repoName = strings.TrimLeft(repoName, "/")
	src, err := op.registry.Repository(ctx, repoName)
	if err != nil {
		log.L().Error("oras: getting repository reference failed", zap.String("repo", repoName), zap.Error(err))
		return nil
	}

	// TODO: tag name
	tagName := "latest"

	// downloads the stuff locally
	fileStorePath, err := os.MkdirTemp(op.fileStoreBasePath, "oras-provider-file-store-*")
	if err != nil {
		log.L().Error("oras: failed to create temporary directory for file store", zap.String("repo", repoName), zap.Error(err))
		return nil
	}
	defer func() {
		if rc == nil {
			log.L().Debug("oras: cleaning up temporary file store path", zap.String("fileStorePath", fileStorePath))
			os.RemoveAll(fileStorePath)
		}
	}()
	fileStore, err := file.New(fileStorePath)
	if err != nil {
		log.L().Error("oras: failed to create file store", zap.String("repo", repoName), zap.Error(err))
		return nil
	}

	rootDesc, err := oras.Copy(ctx, src, tagName, fileStore, tagName, oras.DefaultCopyOptions)
	if err != nil {
		log.L().Error("oras: copying artifact into memory failed", zap.String("repo", repoName), zap.Error(err))
		return nil
	}

	// fetch all entries for the tag
	nodes, err := content.Successors(ctx, fileStore, rootDesc)
	if err != nil {
		log.L().Error("oras: fetching successors failed", zap.String("repo", repoName), zap.Error(err))
		return nil
	}

	if len(nodes) == 1 {
		// we would expect just one layer usually, which means we'll just download that
		// and we'll assume this is the content that we are looking for
		ret, err := fileStore.Fetch(ctx, nodes[0])
		if err != nil {
			log.L().Error("oras: fetch layer content failed", zap.String("repo", repoName), zap.Error(err))
			return nil
		}
		return ret
	} else {
		// otherwise we are looking through all the nodes and look for the first "normal" image layer entry
		for _, node := range nodes {
			if node.MediaType == v1.MediaTypeImageLayer {
				// this is probably the right media type for now
				ret, err := fileStore.Fetch(ctx, node)
				if err != nil {
					log.L().Error("oras: fetch layer content failed", zap.String("repo", repoName), zap.Error(err))
					return nil
				}
				return &orasReadCloser{
					fileStorePath: fileStorePath,
					rc:            ret,
				}
			}
		}
	}

	// artifact not found
	log.L().Error("oras: no image layers in artifact", zap.String("repo", repoName))
	return nil
}

type orasReadCloser struct {
	fileStorePath string
	rc            io.ReadCloser
}

// Close implements io.ReadCloser.
func (orc *orasReadCloser) Close() error {
	err := orc.rc.Close()
	log.L().Debug("oras: ReadCloser: cleaning up temporary file store path on Close", zap.String("fileStorePath", orc.fileStorePath))
	os.RemoveAll(orc.fileStorePath)
	return err
}

// Read implements io.ReadCloser.
func (orc *orasReadCloser) Read(p []byte) (n int, err error) {
	return orc.rc.Read(p)
}

var _ io.ReadCloser = &orasReadCloser{}
