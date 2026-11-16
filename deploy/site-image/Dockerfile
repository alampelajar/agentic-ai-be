# The one image every Website Builder site uses: to build it, to run it, and for the
# gateway in front of the running sites. Build it once per host:
#
#   docker build -t agentic-site-node:1 deploy/site-image
#
# Dependencies come from the lock file and are installed here, with network, by the
# person running this command. Build and run containers get no network at all.
FROM node:24-bookworm-slim

WORKDIR /opt/site
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund && npm cache clean --force
COPY tools ./tools

# Nothing in the image belongs to the user the containers run as (65534), so a site
# cannot change the toolchain even if a mount were writable.
ENV NODE_ENV=production
USER 65534:65534
WORKDIR /workspace
CMD ["node", "--version"]
