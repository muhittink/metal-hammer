FROM golang:1.11-stretch as metal-hammer-builder
RUN apt-get update
RUN apt-get install -y make git
WORKDIR /work
COPY .git /work/
COPY go.mod /work/
COPY go.sum /work/
COPY main.go /work/
COPY cmd /work/cmd/
COPY Makefile /work/
RUN make bin/metal-hammer

FROM golang:1.11-stretch as initrd-builder
RUN apt-get update
RUN apt-get install -y rng-tools e2fsprogs dosfstools gdisk curl gcc
RUN mkdir -p ${GOPATH}/src/github.com/u-root
RUN cd ${GOPATH}/src/github.com/u-root \
 && git clone https://github.com/u-root/u-root
RUN go get github.com/u-root/u-root
WORKDIR /work
COPY metal.key /work/
COPY metal.key.pub /work/
COPY metal-hammer.sh /work/
COPY --from=metal-hammer-builder /work/bin/metal-hammer /work/bin/
RUN u-root \
		-format=cpio -build=bb \
		-files="bin/metal-hammer:bbin/metal-hammer" \
		-files="/sbin/sgdisk:usr/bin/sgdisk" \
		-files="/sbin/mkfs.vfat:sbin/mkfs.vfat" \
		-files="/sbin/mkfs.ext4:sbin/mkfs.ext4" \
		-files="/sbin/mke2fs:sbin/mke2fs" \
		-files="/sbin/mkfs.fat:sbin/mkfs.fat" \
		-files="/usr/sbin/rngd:usr/sbin/rngd" \
		-files="/etc/ssl/certs/ca-certificates.crt:etc/ssl/certs/ca-certificates.crt" \
		-files="metal.key:id_rsa" \
		-files="metal.key.pub:authorized_keys" \
		-files="metal-hammer.sh:bbin/uinit" \
	-o metal-hammer-initrd.img
RUN gzip -f metal-hammer-initrd.img

FROM scratch
COPY --from=metal-hammer-builder /work/bin/metal-hammer /
COPY --from=initrd-builder /work/metal-hammer-initrd.img.gz /