FROM golang:1.14 AS rootlesskit
ADD . /go/src/github.com/rootless-containers/rootlesskit
RUN go build -o /rootlesskit github.com/rootless-containers/rootlesskit/cmd/rootlesskit
RUN go build -o /rootlessctl github.com/rootless-containers/rootlesskit/cmd/rootlessctl

FROM rootlesskit AS test-unit
RUN apt update && apt install -y iproute2 socat netcat-openbsd
CMD ["go","test","-v","-race","github.com/rootless-containers/rootlesskit/..."]

FROM ubuntu:18.04 as build-c
RUN apt update && apt install -y git make gcc automake autotools-dev libtool libglib2.0-dev

# To allow running rootlesskit in a container without CAP_SYS_ADMIN, we need to do either
#  a) install newuidmap/newgidmap with file capabilities rather than SETUID (requires kernel >= 4.14)
#  b) install newuidmap/newgidmap >= 20181028
# We choose b) until kernel >= 4.14 gets widely adopted.
# See https://github.com/shadow-maint/shadow/pull/132 https://github.com/shadow-maint/shadow/pull/138
FROM build-c as idmap
RUN sed -i -e 's/# deb-src/deb-src/' /etc/apt/sources.list \
 && apt update && apt build-dep -y uidmap
RUN git clone https://github.com/shadow-maint/shadow.git /shadow
WORKDIR /shadow
RUN git checkout 42324e501768675993235e03f7e4569135802d18
RUN ./autogen.sh --disable-nls --disable-man --without-audit --without-selinux --without-acl --without-attr --without-tcb --without-nscd \
&& make \
&& cp src/newuidmap src/newgidmap /usr/bin

FROM build-c AS slirp4netns
RUN apt update && apt install -y libcap-dev libseccomp-dev
ARG SLIRP4NETNS_COMMIT=v0.4.0
RUN git clone https://github.com/rootless-containers/slirp4netns.git /slirp4netns && \
  cd /slirp4netns && git checkout ${SLIRP4NETNS_COMMIT} && \
  ./autogen.sh && ./configure && make

# github.com/moby/vpnkit@7dd3dcce7d3d8ffa85d43640f70158583d6fa882 (Jul 20, 2019)
FROM djs55/vpnkit@sha256:4cd1ff4d6555b762ebbad78c9aea1d191854d6550b5d4dcc4ff83a282a657244 AS vpnkit

FROM build-c as vdeplug_slirp
ARG S2ARGVEXECS_COMMIT=880b436157ec5a871cd2ed32c7f7223d9c1e05ee
RUN git clone https://github.com/rd235/s2argv-execs.git /s2argv-execs && \
  cd /s2argv-execs && git checkout ${S2ARGVEXECS_COMMIT} && \
  autoreconf -if && ./configure && make && make install
ARG VDEPLUG4_COMMIT=979eec056a56814b770f49934a127c718dc87a73
RUN git clone https://github.com/rd235/vdeplug4.git /vdeplug4 && \
  cd /vdeplug4 && git checkout ${VDEPLUG4_COMMIT} && \
  autoreconf -if && ./configure && make && make install
ARG LIBSLIRP_COMMIT=37fd650ad7fba7eb0360b1e1d0abf69cac6eb403
RUN git clone https://github.com/rd235/libslirp.git /libslirp && \
  cd /libslirp && git checkout ${LIBSLIRP_COMMIT} && \
  autoreconf -if && ./configure && make && make install
ARG VDEPLUGSLIRP_COMMIT=7ad4ec0871b6cdc1db514958c5e6cbf7d116c85f
RUN git clone https://github.com/rd235/vdeplug_slirp.git /vdeplug_slirp && \
  cd /vdeplug_slirp && git checkout ${VDEPLUGSLIRP_COMMIT} && \
  autoreconf -if && ./configure && make && make install

FROM ubuntu:18.04 AS test-base
# iproute2: for `ip` command that rootlesskit needs to exec
# socat: for `socat` command required for --port-driver=socat
# iperf3: only for benchmark purpose
# busybox: only for debugging purpose
# sudo: only for rootful veth benchmark (for comparison)
RUN apt update && apt install -y iproute2 socat iperf3 busybox sudo libglib2.0-0
COPY --from=idmap /usr/bin/newuidmap /usr/bin/newuidmap
COPY --from=idmap /usr/bin/newgidmap /usr/bin/newgidmap
COPY --from=rootlesskit /rootlesskit /home/user/bin/
COPY --from=rootlesskit /rootlessctl /home/user/bin/
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

FROM test-base AS test-light
COPY --from=slirp4netns /slirp4netns/slirp4netns /home/user/bin/

FROM test-light AS test-full
COPY --from=vpnkit /vpnkit /home/user/bin/vpnkit
COPY --from=vdeplug_slirp /usr/local/bin/* /home/user/bin/
COPY --from=vdeplug_slirp /usr/local/lib/* /home/user/lib/
ADD ./hack/test /home/user/test
WORKDIR /home/user/test
ENTRYPOINT ["./docker-entrypoint.sh"]
