.ONESHELL:
SHA := $(shell git rev-parse --short=8 HEAD)
GITVERSION := $(shell git describe --long --all)
BUILDDATE := $(shell date -Iseconds)
VERSION := $(or ${VERSION},devel)

export GOPROXY := https://gomods.fi-ts.io

BINARY := bin/metal-hammer
INITRD := metal-hammer-initrd.img.lz4

.PHONY: clean initrd

all: $(BINARY)

test:
	GO111MODULE=on \
	go test -v -race -cover $(shell go list ./...)

${BINARY}: clean test
	CGO_ENABLED=0 \
	GO111MODULE=on \
	go build \
		-tags netgo \
		-ldflags "-X 'git.f-i-ts.de/cloud-native/metallib/version.Version=$(VERSION)' \
				  -X 'git.f-i-ts.de/cloud-native/metallib/version.Revision=$(GITVERSION)' \
				  -X 'git.f-i-ts.de/cloud-native/metallib/version.Gitsha1=$(SHA)' \
				  -X 'git.f-i-ts.de/cloud-native/metallib/version.Builddate=$(BUILDDATE)'" \
	-o $@

clean:
	rm -f ${BINARY} ${INITRD}

${INITRD}:
	rm -f ${INITRD}
	docker-make --no-push --Lint

initrd: ${INITRD}

ramdisk:
	u-root \
		-format=cpio -build=bb \
		-files="bin/metal-hammer:bbin/uinit" \
		-files="/sbin/sgdisk:usr/bin/sgdisk" \
		-files="/sbin/mkfs.vfat:sbin/mkfs.vfat" \
		-files="/sbin/mkfs.ext4:sbin/mkfs.ext4" \
		-files="/sbin/mke2fs:sbin/mke2fs" \
		-files="/sbin/mkfs.fat:sbin/mkfs.fat" \
		-files="/sbin/hdparm:sbin/hdparm" \
		-files="/usr/sbin/nvme:usr/sbin/nvme" \
		-files="/etc/ssl/certs/ca-certificates.crt:etc/ssl/certs/ca-certificates.crt" \
		-files="metal.key:id_rsa" \
		-files="metal.key.pub:authorized_keys" \
	-o metal-hammer-initrd.img \
	&& lz4 -f -l metal-hammer-initrd.img metal-hammer-initrd.img.lz4 \
	&& rm -f metal-hammer-initrd.img
