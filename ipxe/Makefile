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
TRUST_BUNDLE ?= $(abspath certs/deploy-ca.pem)
EMBED     = $(WORK)/embed.script
MOK_KEY  ?= ../bootstrap/keys/MOK.priv
MOK_CRT  ?= ../bootstrap/keys/MOK.crt

WORK     := build
SRC      := $(WORK)/ipxe-src

# The deploy server URL baked into the embedded script. MUST be set per
# deployment (see embed.script) — the default is a placeholder.
DEPLOY_URL ?= https://deploy.example.com

all: $(WORK)/ipxe.efi $(WORK)/undionly.kpxe $(WORK)/snponly.efi

$(SRC)/.cloned:
	mkdir -p $(WORK)
	git clone --depth 1 --branch $(IPXE_REF) $(IPXE_REPO) $(SRC)
	touch $@

# Feature overrides use iPXE's supported local-config mechanism
# (config/local/*.h) instead of a context patch against general.h.
# The previous patches/general.h.patch did not even apply against the
# pinned v1.21.1 tree — `make build-ipxe` has never completed — and a
# context patch breaks again every time upstream reflows the file.
$(SRC)/src/config/local/general.h: $(SRC)/.cloned
	mkdir -p $(SRC)/src/config/local
	printf '#define IMAGE_TRUST_CMD\n#define CERT_CMD\n#define NSLOOKUP_CMD\n#define NTP_CMD\n#define DOWNLOAD_PROTO_HTTPS\n' > $@

$(WORK)/embed.script: embed.script
	mkdir -p $(WORK)
	sed 's|https://deploy.example.com|$(DEPLOY_URL)|' $< > $@

$(WORK)/ipxe.efi: $(SRC)/.cloned $(SRC)/src/config/local/general.h $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src NO_WERROR=1 \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(TRUST_BUNDLE) \
		CERT=$(abspath $(CA_CERT)) \
		bin-x86_64-efi/ipxe.efi
	cp $(SRC)/src/bin-x86_64-efi/ipxe.efi $@

$(WORK)/snponly.efi: $(SRC)/.cloned $(SRC)/src/config/local/general.h $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src NO_WERROR=1 \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(TRUST_BUNDLE) \
		CERT=$(abspath $(CA_CERT)) \
		bin-x86_64-efi/snponly.efi
	cp $(SRC)/src/bin-x86_64-efi/snponly.efi $@

$(WORK)/undionly.kpxe: $(SRC)/.cloned $(SRC)/src/config/local/general.h $(EMBED) $(CA_CERT)
	$(MAKE) -C $(SRC)/src NO_WERROR=1 \
		EMBED=$(abspath $(EMBED)) \
		TRUST=$(TRUST_BUNDLE) \
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
