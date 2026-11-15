.PHONY: help install generate check-generated check check-fast frontend server docs dev-admin dev-web dev-server infra-up infra-down smoke-template media-smoke migrate-status migrate-up test-migrate test-source test-content test-media test-upload test-authorization test-staff test-review test-web
help:
	@echo 'install | generate | check-generated | check | check-fast | media-smoke | migrate-status | migrate-up | test-migrate | test-source | test-content | test-media | test-upload | test-authorization | dev-admin | dev-web | dev-server | infra-up | infra-down'
install:
	npm ci
	$(MAKE) -C server init
	cd server && go mod download
generate:
	$(MAKE) -C server all
check-generated:
	bash scripts/check-generated.sh
docs:
	python3 scripts/check-docs.py
frontend:
	npm run lint
	npm run format:check
	npm run type-check
	npm test
	npm run build
server:
	bash scripts/check-go-format.sh
	cd server && go vet ./...
	cd server && go test -race ./...
	$(MAKE) -C server build
check-fast: docs frontend server
check: check-fast check-generated media-smoke
	npm run test:e2e
dev-admin:
	npm run dev:admin
dev-web:
	npm run dev:web
dev-server:
	@test -n "$$KRATOS_DATABASE_SOURCE" || (echo 'Set KRATOS_DATABASE_SOURCE to the NEW development database DSN'; exit 1)
	cd server && go run ./cmd/openduanju-server -conf ./configs
infra-up:
	docker compose --env-file .env -f deploy/compose.yaml up -d mysql redis
infra-down:
	docker compose --env-file .env -f deploy/compose.yaml stop

smoke-template:
	$(MAKE) -C server build
	python3 scripts/smoke-template.py

media-smoke:
	bash scripts/media-smoke.sh

migrate-status:
	cd server && go run ./cmd/openduanju-migrate -conf ./configs -status

migrate-up:
	cd server && go run ./cmd/openduanju-migrate -conf ./configs -up

test-migrate:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required, e.g. MIGRATE_TEST_DSN="user:pass@tcp(127.0.0.1:13380)/openduanju_migrate_test?parseTime=true"' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/migrate -count=1 -v

test-source:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the catalog repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestCatalog' -count=1 -v
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./cmd/openduanju-server -count=1 -v

# test-content covers the working draft, the draft edits, manual creation and
# the new-revision chain: the inheritance, the resubmission through the worker
# and the review gate, and the replacement of an editable draft.
test-content:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the content revision tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestCreateRevision|TestUpdateRevision|TestInheritedMaterial|TestResubmission|TestRejectedRevision|TestManualSeries' -count=1 -v

test-media:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the media batch repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestMedia' -count=1 -v
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestLocalObjectStorage' -count=1 -v
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestLocalStorage' -count=1 -v

# test-upload covers the manual upload sessions and the object version binding.
test-upload:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the upload repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestUpload' -count=1 -v
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestLocalStaging' -count=1 -v

# test-review covers the review worklist, decisions, publication and the login
# throttle.
test-review:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the review repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestReview|TestPublication|TestLoginThrottle' -count=1 -v
	@cd server && go test ./internal/data -run 'TestCOSPreviewURLBindsTheVersion' -count=1 -v
	@cd server && go test ./internal/server -run 'TestCapabilityFor|TestPublishCapability' -count=1 -v

# test-web covers the public projection, the playback session and the
# device-local history rules.
test-web:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the public projection tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestWeb' -count=1 -v

# test-staff covers accounts, password hashing, sessions and the last-admin
# guard.
test-staff:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the staff repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestStaff' -count=1 -v

# test-audit covers the audit trail: the row committed with the change, the
# rollback when it cannot be written, the failure after a rollback, the
# idempotent replay and the read side.
test-audit:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the audit repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestAudit|TestFailedAuditRecord' -count=1 -v
	@cd server && go test ./internal/biz -run 'TestAudit|TestTrace' -count=1 -v
	@cd server && go test ./internal/server -run 'TestTrace|TestCapabilityFor' -count=1 -v

# check-proxy-logs sends a synthetic value to the three signed media endpoints
# through both consoles and fails when it appears in the application or the proxy
# log. The Go test covers the filter; this covers the layers it cannot see, which
# is where nginx would otherwise write the whole request line. It needs the
# running stack and docker, so it is not part of make check.
check-proxy-logs:
	@./scripts/check-proxy-logs.sh $(ADMIN_PORT) $(WEB_PORT) $(SERVER_CONTAINER) $(ADMIN_CONTAINER) $(WEB_CONTAINER)

# test-authorization covers series grants, the submission/review gates and
# revocation.
test-authorization:
	@test -n "$(MIGRATE_TEST_DSN)" || (echo 'MIGRATE_TEST_DSN is required for the authorization repository tests' >&2; exit 2)
	@cd server && TEST_MYSQL_DSN="$(MIGRATE_TEST_DSN)" go test ./internal/data -run 'TestAuthorization' -count=1 -v
