ARG GO_VERSION=1.14
ARG UBUNTU_VERSION=18.04
ARG SHADOW_VERSION=4.8.1
ARG SLIRP4NETNS_VERSION=v0.4.3
# https://github.com/moby/vpnkit/commit/6bc1679a048d5d6837a37043e097f7b7cb710fdb (Mar 4, 2020)
ARG VPNKIT_DIGEST=sha256:e0e226d66cf3a7f2b4c3707aba207d4803be8a4f3a4e070a380de6d737a44669

FROM golang:${GO_VERSION}-alpine AS rootlesskit
RUN apk add --no-cache file
ADD . /go/src/github.com/rootless-containers/rootlesskit
ENV CGO_ENABLED=0
RUN mkdir -p /out && \
  go build -o /out github.com/rootless-containers/rootlesskit/cmd/... && \
  file /out/* | grep -v dynamic

FROM scratch AS artifact
COPY --from=rootlesskit /out/rootlesskit /rootlesskit
COPY --from=rootlesskit /out/rootlessctl /rootlessctl

# `go test -race` requires non-Alpine
FROM golang:${GO_VERSION} AS test-unit
RUN apt-get update && apt-get install -y iproute2 socat netcat-openbsd
ADD . /go/src/github.com/rootless-containers/rootlesskit
WORKDIR /go/src/github.com/rootless-containers/rootlesskit
RUN go mod verify
CMD ["go","test","-v","-race","github.com/rootless-containers/rootlesskit/..."]

# idmap runnable without --privileged (but still requires seccomp=unconfined apparmor=unconfined)
FROM ubuntu:${UBUNTU_VERSION} AS idmap
RUN apt-get update && apt-get install -y automake autopoint bison gettext git gcc libcap-dev libtool make
RUN git clone https://github.com/shadow-maint/shadow.git /shadow
WORKDIR /shadow
ARG SHADOW_VERSION
RUN git pull && git checkout $SHADOW_VERSION
RUN ./autogen.sh --disable-nls --disable-man --without-audit --without-selinux --without-acl --without-attr --without-tcb --without-nscd && \
  make && \
  cp src/newuidmap src/newgidmap /usr/bin

FROM djs55/vpnkit@${VPNKIT_DIGEST} AS vpnkit

FROM ubuntu:${UBUNTU_VERSION} AS test-integration
# iproute2: for `ip` command that rootlesskit needs to exec
# liblxc-common and lxc-utils: for `lxc-user-nic` binary required for --net=lxc-user-nic
# socat: for `socat` command required for --port-driver=socat
# iperf3: only for benchmark purpose
# busybox: only for debugging purpose
# sudo: only for lxc-user-nic benchmark and rootful veth benchmark (for comparison)
# libcap2-bin and curl: used by the RUN instructions in this Dockerfile.
RUN apt-get update && apt-get install -y iproute2 liblxc-common lxc-utils socat iperf3 busybox sudo libcap2-bin curl
COPY --from=idmap /usr/bin/newuidmap /usr/bin/newuidmap
COPY --from=idmap /usr/bin/newgidmap /usr/bin/newgidmap
RUN /sbin/setcap cap_setuid+eip /usr/bin/newuidmap && \
  /sbin/setcap cap_setgid+eip /usr/bin/newgidmap && \
  useradd --create-home --home-dir /home/user --uid 1000 user && \
  mkdir -p /run/user/1000 /etc/lxc && \
  echo "user veth lxcbr0 32" > /etc/lxc/lxc-usernet && \
  echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user
COPY --from=rootlesskit /out/rootlesskit /home/user/bin/
COPY --from=rootlesskit /out/rootlessctl /home/user/bin/
ARG SLIRP4NETNS_VERSION
RUN curl -sSL -o /home/user/bin/slirp4netns https://github.com/rootless-containers/slirp4netns/releases/download/${SLIRP4NETNS_VERSION}/slirp4netns-x86_64 && \
  chmod +x /home/user/bin/slirp4netns
COPY --from=vpnkit /vpnkit /home/user/bin/vpnkit
ADD ./hack /home/user/hack
RUN chown -R user:user /run/user/1000 /home/user
USER user
ENV HOME /home/user
ENV USER user
ENV XDG_RUNTIME_DIR=/run/user/1000
ENV PATH /home/user/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=/home/user/lib
WORKDIR /home/user/hack
