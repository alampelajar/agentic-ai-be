# Registry proxy (dependency installation)

Test and build jobs run with no network. Installing dependencies (`npm install`,
`pip install`, `go mod vendor`) needs the package registries, and nothing else. This
folder builds the one component that makes that safe.

```
install job container ──> cloudexec-install (docker network --internal) ──> cloudexec-registry-proxy ──> registries
```

- `cloudexec-install` is a Docker network created with `--internal`: containers on it have
  no route to the host, to your LAN or to the internet, and public DNS does not resolve.
- `cloudexec-registry-proxy` is the only other member of that network and also sits on the
  default bridge, so it alone can reach the internet. It tunnels HTTPS (`CONNECT`) to
  `registry.npmjs.org`, `proxy.golang.org`, `sum.golang.org`, `pypi.org` and
  `files.pythonhosted.org` on port 443 and refuses everything else, including any name
  that resolves to a private, loopback or link-local address.
- The image contains only the proxy binary (`FROM scratch`, non-root, read-only).

## Set up

On the host where the execution worker runs, from the repository root:

```
bash deploy/registry-proxy/setup.sh
```

It builds the image, creates the network if it is missing (and refuses to reuse one that
is not internal), and starts the proxy with `--restart unless-stopped`. It is safe to run
again. Then give **both the API server and the worker**:

```
CLOUD_EXEC_INSTALL_NETWORK=cloudexec-install
CLOUD_EXEC_INSTALL_PROXY=cloudexec-registry-proxy:3128
```

The worker prints `Dependency installation: on` at start, or says why it is not ready.
Before every install job it checks again that the network is internal and the proxy runs.

## Remove

```
docker rm -f cloudexec-registry-proxy
docker network rm cloudexec-install
```

## Changing what is allowed

`PROXY_ALLOWED_HOSTS` (comma separated host names) on the proxy container replaces the
list; it is a deployment setting and never comes from a request. Keep it to registry
hosts. Every name you add is somewhere an installing project can send requests to.
