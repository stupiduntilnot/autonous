FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

ENV RUSTUP_HOME=/usr/local/rustup \
    CARGO_HOME=/usr/local/cargo \
    PATH=/usr/local/cargo/bin:${PATH}

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    jq \
    nodejs \
    npm \
    python3 \
    tini \
    build-essential \
    pkg-config \
    libssl-dev \
    && rm -rf /var/lib/apt/lists/*

# Rust toolchain (minimal profile for faster image setup).
RUN curl -fsSL https://sh.rustup.rs | bash -s -- -y --profile minimal --default-toolchain stable \
    && rustc --version \
    && cargo --version

# Codex CLI.
RUN npm install -g @openai/codex \
    && codex --version

WORKDIR /app

COPY startup.sh /app/startup.sh
RUN chmod +x /app/startup.sh

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/app/startup.sh"]
