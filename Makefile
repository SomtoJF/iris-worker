start-temporal-server:
	docker compose -f docker/docker-compose.yml up

start-worker:
	CompileDaemon -graceful-kill=true -command="./iris-worker -rod=show,trace,slow=1s" -build="go build -o iris-worker main.go" -exclude-dir="vendor"

clean:
	docker compose -f docker/docker-compose.yml down

.PHONY: start-temporal-server start-worker clean