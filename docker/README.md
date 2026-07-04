# Services

One directory per service. Each contains:

- `deploy.yaml` — the compose file (this repo is public: **no secrets in here, ever**)
- `sample.env` — committed template of the environment file, when the service needs one
- `.env` — the real secrets, **server-side only** at `~/github/infra/docker/<dir>/.env`
  (gitignored; coach overlays it from the mounted services dir when deploying)

## Naming

- **Own services** (built to `registry.baileys.dev`): `github.com_<org>_<repo>`, with
  `github.com_baely_slop_<app>` for apps living in the [slop](https://github.com/baely/slop) monorepo.
- **Community services**: plain names (`authentik`, `immich`, `karakeep`, ...).

**Core services are intentionally NOT managed here**: traefik, portainer, pihole and the
docker registry are the bootstrap layer (ingress, management, DNS, image distribution) —
a bad repo deploy shouldn't be able to take them down. They're managed directly on the server.

## Conventions

- Every `deploy.yaml` sets `name:` to pin the compose project name. Several services
  predate this layout, so the project name preserves their existing volume/network
  prefixes (e.g. `name: traveller` for voyage → volume `traveller_voyage-data`).
- Volumes created by old `docker run` deployments are declared `external: true`.
- Data directories stay where they are (absolute paths, e.g. `/home/user/immich/library`);
  non-secret config files are committed next to the deploy.yaml and bind-mounted relatively.
- Every traefik-routed service joins the external `web` network (in addition to any
  stack-internal networks) and pins `traefik.docker.network=web`. Traefik itself only
  attaches to `web` (plus the default bridge for the hand-managed registry/portainer).
- The shared Postgres (`pg_transactions`) publishes no host port; consumers (traccar, txn)
  reach it as `pg_transactions:5432` over the `pg` network it creates.

## Deploying

Own services are deployed by [coach](../tools/README.md), which pulls `docker/<dir>/`
from GitHub, overlays the server-side `.env`, and runs `docker compose -f deploy.yaml up -d`.

Community services are deployed manually on the server:

```sh
cd ~/github/infra/docker/<dir>
docker compose -f deploy.yaml up -d
```

For containers that still run under their original `docker run` / old compose location,
remove the old container first (`docker rm -f <name>`) — the new compose file reuses the
same container name and volumes, so data carries over.
