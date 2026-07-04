# Services

One directory per service. Each contains:

- `deploy.yaml` — the compose file (this repo is public: **no secrets in here, ever**)
- `sample.env` — committed template of the environment file, when the service needs one
- `.env` — the real secrets, **server-side only** at `~/github/infra/docker/<dir>/.env`
  (gitignored; coach overlays it from the mounted services dir when deploying)

## Naming

- **Own services** (built to `registry.baileys.dev`): `github.com_<org>_<repo>`, with
  `github.com_baely_slop_<app>` for apps living in the [slop](https://github.com/baely/slop) monorepo.
- **Community services**: plain names (`authentik`, `pihole`, `traefik`, ...).

## Conventions

- Every `deploy.yaml` sets `name:` to pin the compose project name. Several services
  predate this layout, so the project name preserves their existing volume/network
  prefixes (e.g. `name: traveller` for voyage → volume `traveller_voyage-data`).
- Volumes created by old `docker run` deployments are declared `external: true`.
- Data directories stay where they are (absolute paths, e.g. `/home/user/immich/library`);
  non-secret config files are committed next to the deploy.yaml and bind-mounted relatively.
- Standalone traefik-routed services join the external `web` network.

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
