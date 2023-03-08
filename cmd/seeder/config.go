package main

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is passed to a seeder instance. It will initialize the seeder based on this configuration.
type Config struct {
	// Servers holds all HTTP server settings
	Servers *Servers `json:"servers,omitempty" yaml:"servers,omitempty"`

	// EmbeddedConfigGenerator contains all settings which are necessary to generate embedded configuration for the
	// staged installer artifacts
	EmbeddedConfigGenerator *EmbeddedConfigGeneratorConfig `json:"embedded_config_generator,omitempty" yaml:"embedded_config_generator,omitempty"`

	// InstallerSettings are various settings that are being used in configurations that are being sent to clients through
	// embedded configurations.
	InstallerSettings *InstallerSettings `json:"installer_settings,omitempty" yaml:"installer_settings,omitempty"`
}

type Servers struct {
	// ServerInsecure will instantiate an insecure server if it is not nil. The insecure server serves
	// all artifacts which are allowed to be served over an unsecured connection like the stage0 installer.
	ServerInsecure *BindInfo `json:"insecure,omitempty" yaml:"insecure,omitempty"`

	// ServerSecure will instantiate a secure server if it is not nil. The secure server serves all artifacts
	// which must be served over a secure connection.
	ServerSecure *BindInfo `json:"secure,omitempty" yaml:"secure,omitempty"`
}

// BindInfo provides all the necessary information for binding to an address and configuring TLS as necessary.
type BindInfo struct {
	// Address is a set of addresses that the server should bind on. In practice multiple HTTP server instances
	// will be running, but all serving the same routes for the same purpose. At least one address must be
	// provided.
	Addresses []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`

	// ClientCAPath points to a file containing one or more CA certificates that client certificates will be
	// validated against if a client certificate is provided. If this is empty, no client authentication will
	// be required on the TLS server. This setting is ignored if no server key and certificate were provided.
	ClientCAPath string `json:"client_ca,omitempty" yaml:"client_ca,omitempty"`

	// ServerKeyPath points to a file containing the server key used for the TLS server. If this is empty,
	// a plain HTTP server will be initiated.
	ServerKeyPath string `json:"server_key,omitempty" yaml:"server_key,omitempty"`

	// ServerCertPath points to a file containing the server certificate used for the TLS server. If `ServerKeyPath`
	// is set, this setting is required to be set.
	ServerCertPath string `json:"server_cert,omitempty" yaml:"server_cert,omitempty"`
}

type EmbeddedConfigGeneratorConfig struct {
	// KeyPath points to a file which contains the key which is being used to sign embedded configuration.
	KeyPath string `json:"config_signature_key,omitempty" yaml:"config_signature_key,omitempty"`

	// CertPath points to a certificate which is used to sign embedded configuration. Its public key must
	// match the key from `KeyPath`.
	CertPath string `json:"config_signature_cert,omitempty" yaml:"config_signature_cert,omitempty"`
}

// InstallerSettings are various settings that are being used in configurations that are being sent to clients through
// embedded configurations
type InstallerSettings struct {
	// ServerCAPath points to a file containing the CA certificate which signed the server certificate which is used
	// for the TLS server. This is necessary to provide it to clients in case they have not received it through an
	// alternative way.
	ServerCAPath string `json:"server_ca,omitempty" yaml:"server_ca,omitempty"`

	// ConfigSignatureCAPath points to a file containing the CA certificate which signed the signature certificate
	// which is used to sign the embedded configuration which is served with every staged installer.
	ConfigSignatureCAPath string `json:"config_signature_ca,omitempty" yaml:"config_signature_ca,omitempty"`

	// SecureServerName is the host name as it should match the TLS SAN for the server certificates that are used by clients to reach the seeder.
	// This server name will be used to generate various URLs which are going to be used in embedded configurations. If the service needs a
	// different port it needs to be included here (e.g. dasboot.example.com:8080).
	SecureServerName string `json:"secure_server_name,omitempty" yaml:"secure_server_name,omitempty"`

	// DNSServers are the DNS servers which will be configured on clients at installation time
	DNSServers []string `json:"dns_servers,omitempty" yaml:"dns_servers,omitempty"`

	// NTPServers are the NTP servers which will be configured on clients at installation time
	NTPServers []string `json:"ntp_servers,omitempty" yaml:"ntp_servers,omitempty"`

	// SyslogServers are the syslog servers which will be configured on clients at installation time
	SyslogServers []string `json:"syslog_servers,omitempty" yaml:"syslog_servers,omitempty"`
}

// ReferenceConfig will be displayed when requested through the CLI
var ReferenceConfig = Config{
	Servers: &Servers{
		ServerInsecure: &BindInfo{
			Addresses: []string{
				"fe80::808f:98ff:fe66:c45c",
			},
		},
		ServerSecure: &BindInfo{
			Addresses: []string{
				"192.168.42.11",
			},
			ClientCAPath:   "/etc/hedgehog/seeder/client-ca-cert.pem",
			ServerKeyPath:  "/etc/hedgehog/seeder/server-key.pem",
			ServerCertPath: "/etc/hedgehog/seeder/server-cert.pem",
		},
	},
	EmbeddedConfigGenerator: &EmbeddedConfigGeneratorConfig{
		KeyPath:  "/etc/hedgehog/seeder/embedded-config-generator-key.pem",
		CertPath: "/etc/hedgehog/seeder/embedded-config-generator-cert.pem",
	},
	InstallerSettings: &InstallerSettings{
		ServerCAPath:          "/etc/hedgehog/seeder/server-ca-cert.pem",
		ConfigSignatureCAPath: "/etc/hedgehog/seeder/embedded-config-generator-ca-cert.pem",
		SecureServerName:      "das-boot.hedgehog.svc.cluster.local",
		DNSServers:            []string{"192.168.42.11", "192.168.42.12"},
		NTPServers:            []string{"192.168.42.11", "192.168.42.12"},
		SyslogServers:         []string{"192.168.42.11"},
	},
}

func marshalReferenceConfig() ([]byte, error) {
	b := &bytes.Buffer{}
	enc := yaml.NewEncoder(b)
	enc.SetIndent(2)
	if err := enc.Encode(&ReferenceConfig); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open '%s': %w", path, err)
	}
	defer f.Close()
	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config yaml decode: %w", err)
	}
	return &cfg, nil
}
