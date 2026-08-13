.PHONY: build run test test-integration lint migrate-up migrate-down compose-up compose-down

build:
	go build -trimpath -o bin/taskflow ./cmd/api

run:
	go run ./cmd/api

test:
	go test -race ./...

test-integration:
	go test -tags=integration -count=1 ./tests/integration/...

lint:
	go vet ./...

migrate-up:
	docker compose exec -T mysql sh -c 'mysql -u"$$MYSQL_USER" -p"$$MYSQL_PASSWORD" "$$MYSQL_DATABASE"' < migrations/000001_init.up.sql

migrate-down:
	docker compose exec -T mysql sh -c 'mysql -u"$$MYSQL_USER" -p"$$MYSQL_PASSWORD" "$$MYSQL_DATABASE"' < migrations/000001_init.down.sql

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down
