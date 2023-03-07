FROM centos:7.7.1908
RUN yum -y update
RUN yum install -y git \
                   tar \
                   wget \
                   make \
                   autoconf \
                   curl-devel \
                   unzip \
                   automake \
                   libtool \
                   glibc-static.i686 \
                   glibc-devel \
                   glibc-devel.i686 \
                   centos-release-scl \
		           devtoolset-7-gcc* \
                   && \
    yum clean all
RUN yum -y install centos-release-scl
RUN yum -y install devtoolset-7-gcc*
SHELL [ "/usr/bin/scl", "enable", "devtoolset-7"]
RUN gcc --version

RUN wget https://dl.google.com/go/go1.19.6.linux-amd64.tar.gz
RUN tar -C /usr/local -xzf go1.19.6.linux-amd64.tar.gz
ENV PATH $PATH:/usr/local/go/bin

# Configure build with Go modules
ENV GO111MODULE=on
ENV GOPROXY=direct

# Build once, to cache all the modules
WORKDIR /tmp/cache

COPY . .
RUN make

# Remove the cached repo, to reduce the capacity of the image
WORKDIR /root
RUN rm -rf /tmp/cache
