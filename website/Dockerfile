# The HEIMDALL website as a container: Tac pages served by ty, one process,
# no runtime build step — the Tailwind sheet is committed and DuVay is
# vendored before the build. Made to be deployed by HEIMDALL itself: see
# deploy/compose.yaml for the declaration an ECS target consumes.
#
# Build from the repository root so the committed site is the context:
#
#   scripts/vendor-duvay.sh            # DuVay is vendored, never committed
#   docker build -t heimdall-website:$(cat VERSION) -f website/Dockerfile website
#
# ── Stage 1: fetch the pinned ty binary ────────────────────────────────────
FROM debian:bookworm-slim AS fetch

ARG TACHYON_VERSION=latest
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    if [ "$TACHYON_VERSION" = "latest" ]; then \
      url="https://github.com/d31ma/TACHYON/releases/latest/download/install.sh"; \
    else \
      url="https://github.com/d31ma/TACHYON/releases/download/${TACHYON_VERSION}/install.sh"; \
    fi; \
    mkdir -p /opt/ty; \
    curl -fsSL "$url" | INSTALL_DIR=/opt/ty sh; \
    /opt/ty/ty --version

# ── Stage 2: the site ──────────────────────────────────────────────────────
FROM debian:bookworm-slim

# curl stays for the container health probe; nothing else is installed.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --home /srv/website --shell /usr/sbin/nologin website

COPY --from=fetch /opt/ty/ty /usr/local/bin/ty
COPY --chown=website:website . /srv/website

# The build fails here, loudly, if DuVay was not vendored: shipping the site
# with token fallbacks would quietly unstyle every DuVay component.
RUN test -f /srv/website/client/shared/assets/duvay/duvay.min.css \
 || { echo "DuVay is not vendored; run scripts/vendor-duvay.sh before building" >&2; exit 1; }

USER website
WORKDIR /srv/website
EXPOSE 8080

# --no-watch: nothing rebuilds in production; the container is immutable.
CMD ["ty", "serve", "--port", "8080", "--no-watch"]
