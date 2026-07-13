# Cloudflare DDNS

Cloudflare DDNS keeps Cloudflare DNS records synchronized with changing public
IP addresses. It uses a central hub so Cloudflare credentials never need to be
distributed to every server.

Each agent receives a unique key tied to one hostname. For example, an agent
created as `name1` can update only the IPv4 `A` record for `name1.site.com`.
The hub discovers the Cloudflare zone ID, manages the DNS records, and preserves
the TTL and proxy settings of existing records.

## Setup

Create a Cloudflare API token with `Zone / Zone / Read` and
`Zone / DNS / Write` permissions. Then set the zone in `compose.hub.yaml`, save
the token in the `CF_API_TOKEN` environment variable, and start the hub:

```sh
export CF_API_TOKEN='YOUR_CLOUDFLARE_TOKEN'
docker compose -f compose.hub.yaml up -d --build
```

Put the hub behind an HTTPS reverse proxy, then create an agent identity:

```sh
docker compose -f compose.hub.yaml exec hub \
  /cloudflare-ddns clients add name1
```

Set the printed key in `CLIENT_TOKEN`, the same client name in `SUBDOMAIN`, and
the hub's zone in `ZONE` on the agent server. Then set `HUB_URL` in
`compose.agent.yaml` and start it:

```sh
export CLIENT_TOKEN='THE_PRINTED_CLIENT_TOKEN'
export SUBDOMAIN='name1'
export ZONE='site.com'
docker compose -f compose.agent.yaml up -d --build
```

Every update includes `SUBDOMAIN` and `ZONE`. The hub verifies that they match
the client identified by `CLIENT_TOKEN` and the hub's configured zone, rejecting
mismatches before changing DNS.

The agent checks its public IPv4 address every five minutes and asks the hub to
update its assigned hostname. Keys can be listed, rotated, or removed through
the hub's `clients` command.

## Development

```sh
go test ./...
go build ./cmd/cloudflare-ddns
```
