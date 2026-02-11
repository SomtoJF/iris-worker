start-temporal-server:
	docker compose -f docker/docker-compose.yml up

start-worker:
	CompileDaemon -command="go run main.go" -build="go build -o iris-worker main.go" -exclude-dir="vendor"

clean:
	docker compose -f docker/docker-compose.yml down

.PHONY: start-temporal-server start-worker clean