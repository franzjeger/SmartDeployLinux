# Build a custom iPXE with HTTPS support and our deploy-server CA pinned
# as a trusted root.
#
# Output artifacts:
#   build/ipxe.efi              -- UEFI x64, for ESP/network boot
#   build/ipxe.efi.signed       -- the same, signed by our MOK key
#   build/undionly.kpxe         -- legacy BIOS PXE (for LAN PXE only)
#   build/snponly.efi           -- UEFI HTTP Boot variant (no UNDI driver)
#
# Both the LAN-PXE artifacts and the bootstrap-embedded artifacts come from
# this same build. The bootstrap-embedded variant differs only in that it
# is invoked by deploy-bootstrap with the chainload URL passed as argv[1];
# both variants have the same trusted-CA set.

SHELL    := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

IPXE_REF ?= v1.21.1
IPXE_REPO?= https://github.com/ipxe/ipxe.git

CA_CERT  ?= certs/deploy-ca.pem
EMBED    ?= embed.script
MOK_KEY  ?= ../bootstrap/keys/MOK.priv
MOK_CRT  ?= ../bootstrap/keys/MOK.crt

WORK     := build
SRC      := $(WORK)/ipxe-src

CONFIG_PATCHES := patches/general.h.patch

all: $(WORK)/ipxe.efi $(WORK)/undionly.kpxe $(WORK)/snponly.efi

$(SRC)/.cloned:
	mkdir -p $(WORK)
	git clone --depth 1 --branch $(IPXE_REF) $(IPXE_REPO) $(SRC)
	touch $@

$(SRC)/src/config/local/general.h: $(SRC)/.cloned $(CONFIG_PATCHES)
	mkdir -p $(SRC)/src/config/local
	cp $(SRC)/src/config/general.h $@.tmp
	# Apply our overrides via patch (kept in patches/ so the diff is
	# reviewable; we do NOT just dump a fresh file because iPXE's defaults
	# move and we want to inherit upstream changes).
	patch -p1 -d $(SRC) < patches/general.h.patch
	mv $@.tmp $@

$(WORK)/ipxe.efi: $(SRC)/.cloned $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(abspath $(CA_CERT)) \
		CERT=$(abspath $(CA_CERT)) \
		bin-x86_64-efi/ipxe.efi
	cp $(SRC)/src/bin-x86_64-efi/ipxe.efi $@

$(WORK)/snponly.efi: $(SRC)/.cloned $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(abspath $(CA_CERT)) \
		CERT=$(abspath $(CA_CERT)) \
		bin-x86_64-efi/snponly.efi
	cp $(SRC)/src/bin-x86_64-efi/snponly.efi $@

$(WORK)/undionly.kpxe: $(SRC)/.cloned $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(abspath $(CA_CERT)) \
		CERT=$(abspath $(CA_CERT)) \
		bin/undionly.kpxe
	cp $(SRC)/src/bin/undionly.kpxe $@

# Sign the EFI binary with our MOK key so it boots under Secure Boot when
# loaded after shim. shim itself is unmodified upstream.
$(WORK)/ipxe.efi.signed: $(WORK)/ipxe.efi
	sbsign --key $(MOK_KEY) --cert $(MOK_CRT) --output $@ $<

clean:
	rm -rf $(WORK)

.PHONY: all clean
