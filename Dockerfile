FROM node:20-bookworm-slim AS web-builder

WORKDIR /build/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

WORKDIR /build/web-wizard
COPY web-wizard/package*.json ./
RUN npm install
COPY web-wizard/ ./
RUN npm run build



FROM ubuntu:22.04 AS go-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ make autoconf automake libtool pkg-config \
    linux-headers-generic git curl ca-certificates \
    zlib1g-dev libfreetype-dev libharfbuzz-dev libjpeg-dev libopenjp2-7-dev \
    unzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://go.dev/dl/go1.26.4.linux-amd64.tar.gz | \
    tar -C /usr/local -xz

ENV PATH=/usr/local/go/bin:$PATH
ENV GOMODCACHE=/go/pkg/mod

WORKDIR /workspace
COPY . .
COPY --from=web-builder /build/web/build ./internal/static/build
COPY --from=web-builder /build/web-wizard/build ./internal/wizard/static
RUN make build-deps && make build



FROM ubuntu:24.04 AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gosu \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -r edub && useradd -r -g edub -d /home/edub -m edub

COPY --from=go-builder /workspace/dev/bin/kushim /usr/local/bin/kushim
COPY --from=go-builder /workspace/dev/bin/edub   /usr/local/bin/edub
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 3000

ENTRYPOINT ["/entrypoint.sh"]
CMD ["edub"]
