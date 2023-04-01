#!/bin/bash
set -e

# NOTE: MAC addresses should be sequential, and not miss a number. The first MAC will be used as the MAC base for ONIE.
#
# Why is this necessary?
#
# By default ONIE is reprogramming MAC addresses on startup based on the MAC base addresses in increments.
# According to Carl Brune this is mainly for historical reason with PowerPC hardware where NICs did not have an EEPROM for
# holding their own MAC addresses, and therefore ONIE was programming them.
# Unfortunately for us, the default behaviour of ONIE is still to reprogram this if I see that right. So let's just deal with it.
#
# See this link for details: https://github.com/opencomputeproject/onie/issues/751#issuecomment-390730344
#
# We also treat "eth0" special in the sense that we are going to use a QEMU "user" network device. All other devices get the "socket" device.
# In SONiC VS the eth0 is *always* the management NIC, so this fits a QEMU user device after all.
DEFAULT_NETDEVS=(
    "devid=eth0 mac=0c:20:12:fe:01:00"
    "devid=eth1 mac=0c:20:12:fe:01:01 local_port=127.0.0.1:21011 dest_port=127.0.0.1:21001"
)

# we cannot pass bash arrays, so we will have to parse that
# taking "devid=" as the indicator that this is a new entry
tmp="$NETDEVS"
NETDEVS=()
if [ -n "$tmp" ]; then
    # count=-1
    for i in $tmp; do
        if [[ "$i" = devid=* ]]; then
            ((count=$count+1))
        fi
        if [ -z "$FIRST_MAC" ] ; then
            if [[ "$i" = mac=* ]]; then
                eval $i
                FIRST_MAC="$mac"
            fi
        fi
        NETDEVS[$count]="${NETDEVS[$count]} $i"
    done
else
    NETDEVS=("${DEFAULT_NETDEVS[@]}")
fi

# now read the base MAC address from the first MAC
IFS=':' read -ra BASEMACADDR <<< "$FIRST_MAC"

### START SETTINGS #########
SWITCH_NAME=${1:-switch}
# NETDEVS - see above
### END SETTINGS ###########

# path where this script resides
SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# executable dependencies for this script
UUIDGEN=$(which uuidgen)
WGET=$(which wget)
UNXZ=$(which unxz)
SWTPM_SETUP=$(which swtpm_setup)
# It's our tool. Get it here: https://github.com/githedgehog/onie-qcow2-eeprom-edit
ONIE_EEPROM_EDIT=$(which onie-qcow2-eeprom-edit)

# let's make a dev folder where we are going to store images
echo -n "Making development folder for storing images: "
mkdir -v -p ${SCRIPT_DIR}/../dev/images
IMAGE_DIR=$( cd -- "${SCRIPT_DIR}/../dev/images" &> /dev/null && pwd )
echo ${IMAGE_DIR}
echo

# download OS and UEFI images, etc.pp.
# TODO: the links below are from my personal ONIE builds which I Uploaded to Google drive.
# Once we build HONIE, we should replace these with dedicated public release links.
echo "Downloading OS and UEFI images..."
if [ -f ${IMAGE_DIR}/onie-kvm_x86_64.qcow2 ]; then
    echo "ONIE kvm_x86_64 image already downloaded: ${IMAGE_DIR}/onie-kvm_x86_64.qcow2.xz"
    echo "Delete this file if you want to download it again. Skipping..."
else
    echo "Downloading ONIE kvm_x86_64 image..."
    # NOTE: google drive links like this cannot be downloaded directly, but need some work. There are answers for this on stackoverflow
    # If the file gets too large, then one needs to use a different URL.
    # Source: https://medium.com/@acpanjan/download-google-drive-files-using-wget-3c2c025a8b99
    #$WGET -O ${IMAGE_DIR}/onie-kvm_x86_64.qcow2.xz https://drive.google.com/file/d/1hHDBYSk_vbPvwt68nb_e9qFzut80Hsg6/view?usp=share_link
    $WGET -O ${IMAGE_DIR}/onie-kvm_x86_64.qcow2.xz "https://docs.google.com/uc?export=download&id=1hHDBYSk_vbPvwt68nb_e9qFzut80Hsg6"
    echo
    echo "Extracting ONIE kvm_x86_64 image now, this may take some time... (unxz ${IMAGE_DIR}/onie-kvm_x86_64.qcow2.xz)"
    $UNXZ ${IMAGE_DIR}/onie-kvm_x86_64.qcow2.xz
fi
if [ -f ${IMAGE_DIR}/onie_efi_code.fd ]; then
    echo "ONIE EFI code flash drive already downloaded: ${IMAGE_DIR}/onie_efi_code.fd"
    echo "Delete this file if you want to download it again. Skipping..."
else
    echo "Downloading ONIE EFI code flash drive..."
    # Secure Boot version
    #$WGET -O ${IMAGE_DIR}/onie_efi_code.fd "https://docs.google.com/uc?export=download&id=1eWs37uWarVhvclv3XmjHfo9Eux8HMa8E"
    # Secure Boot disabled in this one
    $WGET -O ${IMAGE_DIR}/onie_efi_code.fd "https://docs.google.com/uc?export=download&id=1OM8NtqH2MHqjaOkPlbfK6QLly71ld3Xu"
fi
if [ -f ${IMAGE_DIR}/onie_efi_vars.fd ]; then
    echo "Flatcar ONIE EFI variables flash drive already downloaded: ${IMAGE_DIR}/onie_efi_vars.fd"
    echo "Delete this file if you want to download it again. Skipping..."
else
    echo "Downloading ONIE EFI variables flash drive..."
    # Secure Boot version
    #$WGET -O ${IMAGE_DIR}/onie_efi_vars.fd "https://docs.google.com/uc?export=download&id=1Jc7Twu5JY7RIkOCl5hbxrj9AakotAC5c"
    # Secure Boot disabled in this one
    # https://drive.google.com/file/d/1BuJeeaaXg4maNi5kFLQb-0uwR58_ljnx/view?usp=sharing
    $WGET -O ${IMAGE_DIR}/onie_efi_vars.fd "https://docs.google.com/uc?export=download&id=1BuJeeaaXg4maNi5kFLQb-0uwR58_ljnx"
fi
echo

# let's make a dev folder where we generate everything for the switch
echo -n "Making development folder for switch $SWITCH_NAME: "
mkdir -v -p ${SCRIPT_DIR}/../dev/$SWITCH_NAME
DEV_DIR=$( cd -- "${SCRIPT_DIR}/../dev/$SWITCH_NAME" &> /dev/null && pwd )
echo ${DEV_DIR}
echo

# copy downloaded images to destination
echo "Copying images to development directory for switch $SWITCH_NAME..."
cp -v -f ${IMAGE_DIR}/onie-kvm_x86_64.qcow2 ${DEV_DIR}/os.img
cp -v -f ${IMAGE_DIR}/onie_efi_code.fd ${DEV_DIR}/efi_code.fd
cp -v -f ${IMAGE_DIR}/onie_efi_vars.fd ${DEV_DIR}/efi_vars.fd
echo

# generate UUID
echo "Generating UUID for virtual machine..."
$UUIDGEN > ${DEV_DIR}/uuid
echo

# write network devices to disk which will be consumed by every run command
echo "Writing network devices to disk at ${DEV_DIR}/netdevs.txt"
for i in "${NETDEVS[@]}"; do
    echo "$i" >> ${DEV_DIR}/netdevs.txt
done

# edit the ONIE EEPROM
# For example, we will just reuse the just generated UUID
# because ... why not?!
cat << EOF > ${DEV_DIR}/onie-eeprom.yaml
tlvs:
  product_name: Hedgehog ONIE kvm_x86_64 Virtual Machine
  part_number: hh-onie-kvm_x86_64-vm-1
  serial_number: $(< ${DEV_DIR}/uuid)
  mac_base:
$(for i in "${BASEMACADDR[@]}"; do
  echo "  - 0x$i"
done)
  manufacture_date: $(date +"%m/%d/%Y %H:%M:%S")
  device_version: 1
  label_revision: null
  platform_name: x86_64-kvm_x86_64-r0
  onie_version: master-01091853-dirty
  num_macs: ${#NETDEVS[@]}
  manufacturer: Caprica Systems
  country_code: US
  vendor: Hedgehog
  diag_version: null
  service_tag: null
  vendor_extension: null
EOF
echo "ONIE EEPROM: Initializing ONIE EEPROM now from values at ${DEV_DIR}/onie-eeprom.yaml"
$ONIE_EEPROM_EDIT --log-level=debug write --force --input ${DEV_DIR}/onie-eeprom.yaml ${DEV_DIR}/os.img
echo

# initialize software TPM
echo "Initializing software TPM 2.0 now..."
$SWTPM_SETUP --create-config-files skip-if-exist
if ! [ -f ${HOME}/.config/swtpm_setup.conf ]; then
    echo "ERROR: swtpm config files expected at: ${HOME}/.config/swtpm_setup.conf" 1>&2
    exit 1
fi
mkdir -p $DEV_DIR/tpm
$SWTPM_SETUP \
  --tpm2 \
  --tpmstate $DEV_DIR/tpm \
  --createek \
  --decryption \
  --create-ek-cert \
  --create-platform-cert \
  --create-spk \
  --vmid "$SWITCH_NAME" \
  --overwrite \
  --display
echo
