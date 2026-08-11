# Mokka Control Plane: Init

We are introducing a new microservice in 
the Mokka project according to the `enhancements/meps/0001-mokka-control-plane/README.md` specification.

The scope of this specific work:

- deployable to a local Kind cluster via tilt and disabled by default (until we release it).
- the component should be packed via nvml-mock helm chart.
- add a separate docker image for Control Plane
- Add a basic REST API for health checks using chi router
- Setup signal handling and graceful termination of the microservice
- use https://github.com/urfave/cli for CLI
- setup logging based on slog

We should break it down into a digestable tasks, so I can review the changes gradually and commit them.
