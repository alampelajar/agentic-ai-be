# Website Builder: testing publish with a real domain

Published sites are served from this computer through Cloudflare Tunnel: no public
IP, no open router port, and Cloudflare provides HTTPS. Previews and the looks'
examples use the same domain: `p-<site>-<signed>.example.id` and
`contoh-<look>.example.id`. The sites are online only while this computer, Docker,
the site runner and the tunnel are running. This is a test setup, not production.

`example.id` below stands for the domain chosen for the sites. Use a domain that is
separate from the app's own (section 4 of `WEBSITE-BUILDER.md`, "Domains").

## 1. Move the domain's DNS to Cloudflare (once)

1. Create a free Cloudflare account and choose "Add a domain", then enter `example.id`
   and choose the Free plan.
2. Cloudflare shows two nameservers. In the registrar's panel, replace the domain's
   nameservers with those two.
3. Wait until Cloudflare shows the domain as "Active" (minutes to a few hours).

Cloudflare's free certificate covers `example.id` and `*.example.id`, one level deep.
So sites go directly under the domain (`toko-roti.example.id`), not under a further
level such as `toko-roti.sites.example.id`, which the free certificate does not cover.

## 2. Create the tunnel (once)

```
winget install --id Cloudflare.cloudflared
cloudflared tunnel login                                   # opens the browser; pick example.id
cloudflared tunnel create agentic-sites                    # prints the tunnel ID and a credentials file
cloudflared tunnel route dns agentic-sites "*.example.id"  # one wildcard record, nothing per site
```

Create `%USERPROFILE%\.cloudflared\config.yml`:

```yaml
tunnel: agentic-sites
credentials-file: C:\Users\<you>\.cloudflared\<tunnel-id>.json
ingress:
  - hostname: "*.example.id"
    service: http://localhost:8090
  - service: http_status:404
```

The tunnel passes the original host name on, and the gateway finds the site by it.
Only the gateway is reachable from the internet; the app and the API are not. A
preview opens only with its signed, two-hour name, which the builder hands out to the
site's owner.

## 3. Point the backend at the domain

In the backend's `.env`:

```
SITES_DOMAIN=example.id
SITE_PREVIEW_DOMAIN=example.id
SITE_PUBLIC_SCHEME=https
SITE_PUBLIC_PORT=
```

Then restart the API server and the site runner. The site runner replaces the
gateway container by itself, because its configuration changed.

## 4. Run it and test

```
cloudflared tunnel run agentic-sites
```

Then, in the builder:

1. Publish a finished site under a name.
2. Open `https://<name>.example.id` from a phone on mobile data (not this computer's
   network) and check that it loads over HTTPS with its pictures and form.
3. Send a form and check that the answer is stored.
4. Restart the site runner and check that the site still answers.
5. Restore an older version and publish it again (stage 5's exit check).
6. Take the site offline and check that the address shows "There is no website at
   this address".

## Going back to local only

Stop the tunnel, remove the four lines from `.env`, and restart the API server and
the site runner.
Sites published under the real domain keep their names. They answer again under
`*.sites.localhost:8090`.
