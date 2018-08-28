FROM golang:1.10 AS rootlesskit
ADD . /go/src/github.com/rootless-containers/rootlesskit
RUN go build -o /rootlesskit github.com/rootless-containers/rootlesskit/cmd/rootlesskit

FROM ubuntu as build-c
RUN apt update && apt install -y git make gcc automake autotools-dev libtool

FROM build-c AS slirp4netns
ARG SLIRP4NETNS_COMMIT=0037042e2facc8818d0a254e320a739648283f2e
RUN git clone https://github.com/rootless-containers/slirp4netns.git /slirp4netns && \
  cd /slirp4netns && git checkout ${SLIRP4NETNS_COMMIT} && \
  ./autogen.sh && ./configure && make

FROM ubuntu AS vpnkit
RUN apt update && apt install -y ocaml opam
ARG VPNKIT_COMMIT=bbedcf42d9e6ba0afba65aca18dd5fc087192b13
RUN git clone https://github.com/moby/vpnkit.git /vpnkit && \
  cd /vpnkit && git checkout ${VPNKIT_COMMIT} && \
  make

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

FROM ubuntu AS test
# busybox is only for debugging purpose
# sudo is only for rootful veth benchmark (for comparison)
RUN apt update && apt install -y iperf3 uidmap iproute2 busybox sudo
RUN useradd --create-home --home-dir /home/user --uid 1000 user \
  && mkdir -p /run/user/1000 \
  && echo "user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/user
COPY --from=rootlesskit /rootlesskit /home/user/bin/
COPY --from=slirp4netns /slirp4netns/slirp4netns /home/user/bin/
COPY --from=vpnkit /vpnkit/vpnkit.exe /home/user/bin/vpnkit
COPY --from=vdeplug_slirp /usr/local/bin/* /home/user/bin/
COPY --from=vdeplug_slirp /usr/local/lib/* /home/user/lib/
ADD ./hack/test/docker-entrypoint.sh /home/user/bin/
RUN chown -R user:user /run/user/1000 /home/user
USER user
ENV HOME /home/user
ENV USER user
ENV XDG_RUNTIME_DIR=/run/user/1000
ENV PATH /home/user/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_LIBRARY_PATH=/home/user/lib
ENTRYPOINT ["/home/user/bin/docker-entrypoint.sh"]
