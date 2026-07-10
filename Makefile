start-temporal-server:
	docker compose -f docker/docker-compose.yml up

start-worker:
	CompileDaemon -graceful-kill=true -command="./iris-worker -rod=show,trace,slow=1s" -build="go build -o iris-worker main.go" -exclude-dir="vendor"

# Headed worker with fixed CDP port for Chrome DevTools MCP observation.
# MCP should use: npx chrome-devtools-mcp@latest --browserUrl http://127.0.0.1:9222
start-worker-observe:
	CompileDaemon -graceful-kill=true -command="./iris-worker -rod=show,trace,slow=1s,port=9222" -build="go build -o iris-worker main.go" -exclude-dir="vendor"

clean:
	docker compose -f docker/docker-compose.yml down

# Docker image (context: repo root). Run: make docker-build && make docker-run
docker-build:
	docker build -f docker/Dockerfile -t iris-worker:local .

docker-run:
	docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  --env-file .env \
  -e RENDER=true \
  -e REDIS_HOST=host.docker.internal:6379 \
  iris-worker:local

# Linux: Temporal/Redis on localhost. Docker Desktop on Mac does not support host networking.
docker-run-host:
	docker run --rm --network host \
		-e RENDER=true \
		-e REDIS_HOST=127.0.0.1:6379 \
		iris-worker:local

.PHONY: start-temporal-server start-worker start-worker-observe clean docker-build docker-run docker-run-host