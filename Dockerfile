ARG GO_VERSION=1.18
ARG UBUNTU_VERSION=20.04
ARG SHADOW_VERSION=4.8.1
ARG SLIRP4NETNS_VERSION=v1.1.12
ARG VPNKIT_VERSION=0.5.0
ARG DOCKER_VERSION=20.10.13

FROM golang:${GO_VERSION}-alpine AS build
RUN apk add --no-cache file git make
ADD . /go/src/github.com/rootless-containers/rootlesskit
WORKDIR /go/src/github.com/rootless-containers/rootlesskit

FROM build AS rootlesskit
RUN CGO_ENABLED=0 make && file /bin/* | grep -v dynamic

FROM scratch AS artifact
COPY --from=rootlesskit /go/src/github.com/rootless-containers/rootlesskit/bin/* /

FROM build AS cross
RUN make cross

FROM scratch AS cross-artifact
COPY --from=cross /go/src/github.com/rootless-containers/rootlesskit/_artifact/* /

# `go test -race` requires non-Alpine
FROM golang:${GO_VERSION} AS test-unit
RUN apt-get update && apt-get install -y git iproute2 netcat-openbsd
ADD . /go/src/github.com/rootless-containers/rootlesskit
WORKDIR /go/src/github.com/rootless-containers/rootlesskit
RUN go mod verify && go vet ./...
CMD ["go","test","-v","-race","github.com/rootless-containers/rootlesskit/..."]

# idmap runnable without --privileged (but still requires seccomp=unconfined apparmor=unconfined)
FROM ubuntu:${UBUNTU_VERSION} AS idmap
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y automake autopoint bison gettext git gcc libcap-dev libtool make
RUN git clone https://github.com/shadow-maint/shadow.git /shadow
WORKDIR /shadow
ARG SHADOW_VERSION
RUN git pull && git checkout $SHADOW_VERSION
RUN ./autogen.sh --disable-nls --disable-man --without-audit --without-selinux --without-acl --without-attr --without-tcb --without-nscd && \
  make && \
  cp src/newuidmap src/newgidmap /usr/bin

FROM djs55/vpnkit:${VPNKIT_VERSION} AS vpnkit

FROM ubuntu:${UBUNTU_VERSION} AS test-integration
# iproute2: for `ip` command that rootlesskit needs to exec
# liblxc-common and lxc-utils: for `lxc-user-nic` binary required for --net=lxc-user-nic
# iperf3: only for benchmark purpose
# busybox: only for debugging purpose
# sudo: only for lxc-user-nic benchmark and rootful veth benchmark (for comparison)
# libcap2-bin and curl: used by the RUN instructions in this Dockerfile.
RUN apt-get update && apt-get install -y iproute2 liblxc-common lxc-utils iperf3 busybox sudo libcap2-bin curl
COPY --from=idmap /usr/bin/newuidmap /usr/bin/newuidmap
COPY --from=idmap /usr/bin/newgidmap /usr/bin/newgidmap
RUN /sbin/setcap cap_setuid+eip /usr/bin/newuidmap && \
  /sbin/setcap cap_setgid+eip /usr/bin/newgidmap && \
  useradd --create-home --home-dir /home/user --uid 1000 user && \
  mkdir -p /run/user/1000 /etc/lxc && \
  echo "user veth lxcbr0 32" > /etc/lxc/lxc-usernet && \
  echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user
COPY --from=artifact /rootlesskit /home/user/bin/
COPY --from=artifact /rootlessctl /home/user/bin/
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

FROM test-integration AS test-integration-docker
COPY --from=artifact /rootlesskit-docker-proxy /home/user/bin/
ARG DOCKER_VERSION
RUN curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz | tar xz --strip-components=1 -C /home/user/bin/
RUN curl -fsSL -o /home/user/bin/dockerd-rootless.sh https://raw.githubusercontent.com/moby/moby/v${DOCKER_VERSION}/contrib/dockerd-rootless.sh && \
  chmod +x /home/user/bin/dockerd-rootless.sh
ENV DOCKERD_ROOTLESS_ROOTLESSKIT_NET=slirp4netns
ENV DOCKERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=builtin
ENV DOCKER_HOST=unix:///run/user/1000/docker.sock
RUN mkdir -p /home/user/.local
VOLUME /home/user/.local
CMD ["dockerd-rootless.sh"]
