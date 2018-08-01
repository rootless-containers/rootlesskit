FROM golang:1.10 AS rootlesskit
ADD . /go/src/github.com/rootless-containers/rootlesskit
RUN go build -o /rootlesskit github.com/rootless-containers/rootlesskit/cmd/rootlesskit

FROM ubuntu as build-c
RUN apt update && apt install -y git make gcc automake autotools-dev libtool

FROM build-c AS slirp4netns
RUN git clone https://github.com/rootless-containers/slirp4netns.git /slirp4netns
WORKDIR /slirp4netns
ARG SLIRP4NETNS_COMMIT=c4e1bc5a5e6987f3a352ca524f13320a2d483398
RUN git checkout ${SLIRP4NETNS_COMMIT} && ./autogen.sh && ./configure && make

FROM ubuntu AS vpnkit
RUN apt update && apt install -y ocaml opam
RUN git clone https://github.com/moby/vpnkit.git /vpnkit
WORKDIR /vpnkit
ARG VPNKIT_COMMIT=bbedcf42d9e6ba0afba65aca18dd5fc087192b13
RUN git checkout ${VPNKIT_COMMIT} && make

FROM build-c as vdeplug_slirp
RUN git clone https://github.com/rd235/s2argv-execs.git /s2argv-execs
WORKDIR /s2argv-execs
ARG S2ARGVEXECS_COMMIT=880b436157ec5a871cd2ed32c7f7223d9c1e05ee
RUN git checkout ${S2ARGVEXECS_COMMIT} && autoreconf -if && ./configure && make && make install
RUN git clone https://github.com/rd235/vdeplug4.git /vdeplug4
WORKDIR /vdeplug4
ARG VDEPLUG4_COMMIT=979eec056a56814b770f49934a127c718dc87a73
RUN git checkout ${VDEPLUG4_COMMIT} && autoreconf -if && ./configure && make && make install
RUN git clone https://github.com/rd235/libslirp.git /libslirp
WORKDIR /libslirp
ARG LIBSLIRP_COMMIT=37fd650ad7fba7eb0360b1e1d0abf69cac6eb403
RUN git checkout ${LIBSLIRP_COMMIT} && autoreconf -if && ./configure && make && make install
RUN git clone https://github.com/rd235/vdeplug_slirp.git /vdeplug_slirp
WORKDIR /vdeplug_slirp
ARG VDEPLUGSLIRP_COMMIT=7ad4ec0871b6cdc1db514958c5e6cbf7d116c85f
RUN git checkout ${VDEPLUGSLIRP_COMMIT} && autoreconf -if && ./configure && make && make install

FROM ubuntu AS test
# busybox is only for debugging purpose
RUN apt update && apt install -y iperf3 uidmap iproute2 busybox
RUN useradd --create-home --home-dir /home/user --uid 1000 user \
  && mkdir -p /run/user/1000
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
CMD ["/home/user/bin/docker-entrypoint.sh"]
