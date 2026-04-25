# Build the USB bootstrap image.
#
# This Makefile orchestrates buildroot to produce kernel + initramfs, then
# assembles a hybrid GPT/MBR disk image with the ESP for UEFI boot and a
# Linux partition for the kernel/initramfs/overlay.
#
# Output:
#   output/deploy-bootstrap.img       -- flashable disk image
#   output/deploy-bootstrap.img.sha256
#
# Two profiles:
#   PROFILE=slim   (default) wired-only, ~90 MB
#   PROFILE=fat              wired + Wi-Fi (linux-firmware blobs), ~600 MB

SHELL    := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

PROFILE  ?= slim
WORK     := output
BR_DIR   := $(WORK)/buildroot-2025.02
BR_CONFIG:= buildroot/configs/bootstrap_$(PROFILE)_defconfig
OVERLAY  := overlay
KEYS_DIR := keys

KERNEL   := $(BR_DIR)/output/images/bzImage
ROOTFS   := $(BR_DIR)/output/images/rootfs.cpio.gz

IPXE_DIR := ../ipxe/build
IPXE_EFI := $(IPXE_DIR)/ipxe.efi.signed
IPXE_BIOS:= $(IPXE_DIR)/undionly.kpxe

IMG      := $(WORK)/deploy-bootstrap.img
IMG_SIZE := 320M

all: $(IMG) $(IMG).sha256

$(BR_DIR):
	mkdir -p $(WORK)
	curl -fsSL https://buildroot.org/downloads/buildroot-2025.02.tar.xz \
	    -o $(WORK)/buildroot.tar.xz
	tar -xJf $(WORK)/buildroot.tar.xz -C $(WORK)
	rm $(WORK)/buildroot.tar.xz

$(BR_DIR)/.config: $(BR_DIR) $(BR_CONFIG)
	cp $(BR_CONFIG) $(BR_DIR)/.config
	$(MAKE) -C $(BR_DIR) olddefconfig

$(KERNEL) $(ROOTFS): $(BR_DIR)/.config
	# Stage our overlay into buildroot's rootfs overlay tree.
	rm -rf $(BR_DIR)/board/overlay
	cp -r $(OVERLAY) $(BR_DIR)/board/overlay
	# Stage tailscaled static binary fetched via Makefile target.
	$(MAKE) -C $(BR_DIR) BR2_ROOTFS_OVERLAY=board/overlay

$(IMG): $(KERNEL) $(ROOTFS) $(IPXE_EFI) $(IPXE_BIOS) | $(WORK)
	./scripts/assemble-image.sh \
	    --output      $@ \
	    --size        $(IMG_SIZE) \
	    --kernel      $(KERNEL) \
	    --initramfs   $(ROOTFS) \
	    --ipxe-efi    $(IPXE_EFI) \
	    --ipxe-bios   $(IPXE_BIOS) \
	    --keys-dir    $(KEYS_DIR)

$(IMG).sha256: $(IMG)
	cd $(WORK) && sha256sum $$(basename $(IMG)) > $$(basename $@)

clean:
	rm -rf $(WORK)

$(WORK):
	mkdir -p $@

.PHONY: all clean
