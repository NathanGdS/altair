# Examples

Each folder under `examples/` is a consumer that registers a webhook with Altair. Any example that follows the contract below can be validated by the Docker perf stack (`perf/`) without changing the stack itself:

```bash
./perf/validate.sh smoke 01-consumer     # one example
./perf/validate.sh heavy all             # every example, each on a fresh stack, then a summary
.\perf\validate.ps1 -Mode heavy -Example all
```

## Contract

An example `examples/<name>/` must:

1. **Be named** with lowercase letters, digits, `-` and `_` only. The name becomes the image name `altair-example-<name>:perf`.
2. **Ship a `Dockerfile`** built with the **repository root** as context (`docker build -f examples/<name>/Dockerfile .`). It can use any language. Its entrypoint receives the flags below as arguments.
3. **Accept these flags:**

   | Flag | Value in the perf stack | Meaning |
   |------|-------------------------|---------|
   | `-altair-url` | `http://altair:8080` | broker base URL |
   | `-webhook-host` | `consumer` | host Altair uses to reach the webhook |
   | `-port` | `9090` | port the webhook listens on |
   | `-origin` | `ORIGIN` from `perf.env` | origin to register for |

   Extra flags come from `CONSUMER_ARGS`.
4. **Register on startup** with `POST /consumers`, heartbeat within `ConsumerHeartbeatTTL`, and receive deliveries on `POST /webhook`.
5. **Expose `GET /stats`** on the same port, returning `{"received": <deliveries received so far>}`. It must only answer once registration succeeded: the scripts fire the load as soon as `/stats` answers.

The image needs no tooling of its own (no `wget`/`curl`): the scripts probe `/stats` from the altair container over the stack network.

## `perf.env` (optional)

Per-example expectations and limits, as plain `KEY=VALUE` lines: no quotes, no shell expansion, `#` for comments. Env vars given on the command line override the file, and the file overrides the defaults below. An unknown key is an error, so typos fail fast.

| Key | Default | Meaning |
|-----|---------|---------|
| `ORIGIN` | `stress-test` | origin the consumer registers for and the loader publishes to (`[A-Za-z0-9_-]`) |
| `EXPECT` | `auto` | delivery check: `auto` (smoke → `exact`, heavy → `at-least`), `exact`, `at-least`, `none` |
| `EXPECT_FAILED` | `empty` | `deliveries/failed` check: `empty`, `non-empty` (example fails on purpose), `any` |
| `DRAIN_IDLE_TIMEOUT` | `30` | seconds without a new delivery before giving up on draining |
| `CONSUMER_CPUS` / `CONSUMER_MEM` | `1` / `512m` | container limits for this example |
| `CONSUMER_ARGS` | _(empty)_ | extra flags appended to the contract flags |

`at-least` exists because delivery is at-least-once: a webhook that exceeds `DeliveryHTTPTimeout` gets retried even if the first attempt eventually succeeded, so duplicates are expected under heavy load.

Example for a hypothetical slow webhook:

```env
# Slow webhook: deliveries take longer to drain and some hit DeliveryHTTPTimeout,
# which causes retry duplicates, so the count is only required to be at least `accepted`.
ORIGIN=slow-consumer
EXPECT=at-least
EXPECT_FAILED=empty
DRAIN_IDLE_TIMEOUT=120
CONSUMER_ARGS=-delay=200ms
```
