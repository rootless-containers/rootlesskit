ARG GO_VERSION=1.14
ARG SHADOW_VERSION=4.8.1

FROM golang:${GO_VERSION}-alpine3.11 AS rootlesskit
ADD . /go/src/github.com/rootless-containers/rootlesskit
RUN apk add --no-cache file
ENV CGO_ENABLED=0
RUN mkdir -p /out && \
  go build -o /out github.com/rootless-containers/rootlesskit/cmd/... && \
  file /out/* | grep -v dynamic

FROM scratch AS artifact
COPY --from=rootlesskit /out/rootlesskit /rootlesskit
COPY --from=rootlesskit /out/rootlessctl /rootlessctl

# `go test -race` requires non-Alpine
FROM golang:${GO_VERSION} AS test-unit
ADD . /go/src/github.com/rootless-containers/rootlesskit
RUN apt update && apt install -y iproute2 socat netcat-openbsd
CMD ["go","test","-v","-race","github.com/rootless-containers/rootlesskit/..."]

FROM ubuntu:18.04 as build-c
RUN apt update && apt install -y git make gcc automake autotools-dev libtool libglib2.0-dev

# idmap runnable without --privileged (but still requires seccomp=unconfined apparmor=unconfined)
FROM build-c AS idmap
RUN apt update && apt install -y autopoint bison gettext libcap-dev
RUN git clone https://github.com/shadow-maint/shadow.git /shadow
WORKDIR /shadow
ARG SHADOW_VERSION
RUN git checkout $SHADOW_VERSION
RUN ./autogen.sh --disable-nls --disable-man --without-audit --without-selinux --without-acl --without-attr --without-tcb --without-nscd && \
  make && \
  cp src/newuidmap src/newgidmap /usr/bin

FROM build-c AS slirp4netns
RUN apt update && apt install -y libcap-dev libseccomp-dev
ARG SLIRP4NETNS_COMMIT=v0.4.0
RUN git clone https://github.com/rootless-containers/slirp4netns.git /slirp4netns && \
  cd /slirp4netns && git checkout ${SLIRP4NETNS_COMMIT} && \
  ./autogen.sh && ./configure && make

# github.com/moby/vpnkit@7dd3dcce7d3d8ffa85d43640f70158583d6fa882 (Jul 20, 2019)
FROM djs55/vpnkit@sha256:4cd1ff4d6555b762ebbad78c9aea1d191854d6550b5d4dcc4ff83a282a657244 AS vpnkit

FROM ubuntu:18.04 AS test-integration
# iproute2: for `ip` command that rootlesskit needs to exec
# socat: for `socat` command required for --port-driver=socat
# iperf3: only for benchmark purpose
# busybox: only for debugging purpose
# sudo: only for rootful veth benchmark (for comparison)
RUN apt update && apt install -y iproute2 socat iperf3 busybox sudo libglib2.0-0
COPY --from=idmap /usr/bin/newuidmap /usr/bin/newuidmap
COPY --from=idmap /usr/bin/newgidmap /usr/bin/newgidmap
COPY --from=rootlesskit /out/rootlesskit /home/user/bin/
COPY --from=rootlesskit /out/rootlessctl /home/user/bin/
RUN chmod u+s /usr/bin/newuidmap /usr/bin/newgidmap \
  && useradd --create-home --home-dir /home/user --uid 1000 user \
  && mkdir -p /run/user/1000 \
  && chown -R user:user /run/user/1000 /home/user \
  && echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user
USER user
ENV HOME /home/user
ENV USER user
ENV XDG_RUNTIME_DIR=/run/user/1000
ENV PATH /home/user/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=/home/user/lib
COPY --from=slirp4netns /slirp4netns/slirp4netns /home/user/bin/
COPY --from=vpnkit /vpnkit /home/user/bin/vpnkit
ADD ./hack /home/user/hack
WORKDIR /home/user/hack
