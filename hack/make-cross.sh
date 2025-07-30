#!/bin/sh
set -eux

cd "$(dirname $0)/.."
CGO_ENABLED=0
export CGO_ENABLED

rm -rf _artifact
mkdir -p _artifact

x() {
	goarch="$1"
	uname_m="$2"
	rm -rf bin
	GOARCH="$goarch" make all
	file bin/* | grep -v dynamic
	(cd bin && tar czvf "../_artifact/rootlesskit-${uname_m}.tar.gz" *)
}

x amd64 x86_64
x arm64 aarch64
x s390x s390x
x ppc64le ppc64le
x riscv64 riscv64
GOARM=7
export GOARM
# Skip building for arm due to gvisor-tap-vsock(v0.8.7) incompatibility on 32-bit ARM (see https://github.com/containers/gvisor-tap-vsock/issues/545))
#x arm armv7l

rm -rf bin
