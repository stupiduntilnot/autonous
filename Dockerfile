FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive
ARG GO_VERSION=1.26.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    coreutils \
    curl \
    fd-find \
    gcc \
    git \
    jq \
    libc6-dev \
    perl \
    ripgrep \
    libsqlite3-dev \
    sqlite3 \
    tini \
    && rm -rf /var/lib/apt/lists/*

# Debian package exposes fd as "fdfind"; provide the common "fd" name.
RUN ln -sf /usr/bin/fdfind /usr/local/bin/fd

RUN ARCH=$(dpkg --print-architecture) && \
    case "$ARCH" in \
      amd64) GOARCH=amd64 ;; \
      arm64) GOARCH=arm64 ;; \
      *) echo "unsupported arch: $ARCH" && exit 1 ;; \
    esac && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz

ENV PATH="/usr/local/go/bin:/usr/local/go/bin:${PATH}"

WORKDIR /workspace

COPY startup.sh /usr/local/bin/startup.sh
RUN chmod +x /usr/local/bin/startup.sh

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/startup.sh"]
