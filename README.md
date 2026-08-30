# iris-worker

Temporal worker for Iris. Runs job applications in the browser, job discovery, resume processing, cover letters, captcha solving, and related workflows.

Expects [iris-api](https://github.com/SomtoJF/iris-api) as a sibling checkout (`../iris-api`) because of the `replace` in `go.mod`. After changing `iris-api/model`, run `go mod vendor` here.

## Run

Needs Docker, Go, and the same Postgres as iris-api. Hot reload uses [CompileDaemon](https://github.com/githubnemo/CompileDaemon):

```bash
go install github.com/githubnemo/CompileDaemon@latest
```

Start Temporal (UI at [http://localhost:8081](http://localhost:8081)):

```bash
make start-temporal-server
```

In another terminal:

```bash
cp sample.env .env
make start-worker
```

That builds with hot reload and opens a headed Chromium. For a fixed CDP port (`9222`) so you can attach DevTools:

```bash
make start-worker-observe
```

Without hot reload: `go run main.go`.
