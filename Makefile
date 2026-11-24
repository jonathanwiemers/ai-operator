IMG ?= ghcr.io/jonathanwiemers/ai-operator:latest
AGENT_IMG ?= ghcr.io/jonathanwiemers/ai-operator-agent:latest
CONTROLLER_GEN ?= $(CURDIR)/bin/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.23.0
HELM ?= helm
CHART := charts/ai-operator
# Chart version for helm-package. Releases pass the tag (YEAR.MONTH.N, see
# docs/contributing/releasing.md); the operator picks it, never the build.
VERSION ?= 0.0.0-dev

.PHONY: all
all: generate manifests fmt vet test build

$(CONTROLLER_GEN):
	GOBIN=$(CURDIR)/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Generate deepcopy code.
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./api/...

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate CRDs and RBAC.
	$(CONTROLLER_GEN) rbac:roleName=ai-operator crd paths=./... \
		output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	rm -f $(CHART)/files/crds/*.yaml
	cp config/crd/bases/*.yaml $(CHART)/files/crds/
	cp config/rbac/role.yaml $(CHART)/files/rbac/role.yaml

.PHONY: fmt
fmt:
	gofmt -w api cmd internal

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -race -timeout 20m ./...

.PHONY: coverage-baseline
coverage-baseline: ## Rewrite hack/coverage-baseline.txt from coverage.out (the artifact of the CI test job).
	go run ./hack/coverage -profile coverage.out -write hack/coverage-baseline.txt

.PHONY: test-db
test-db: ## Start a Postgres for the research store tests (docker, 127.0.0.1:55432).
	docker run -d --rm --name ai-operator-test-db -p 127.0.0.1:55432:5432 \
		-e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ai_operator_test postgres:17 > /dev/null
	@echo 'export AI_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/ai_operator_test?sslmode=disable'

.PHONY: research-seed
research-seed: ## Migrate and seed a research database on the test-db Postgres for make run-ui (see docs/contributing/web-ui.md "Development").
	go run ./hack/research-migrate -seed hack/research-seed.sql

.PHONY: test-db-stop
test-db-stop: ## Stop the Postgres of test-db.
	docker stop ai-operator-test-db

.PHONY: build
build:
	go build -o bin/manager ./cmd/operator
	go build -o bin/ai-runner ./cmd/runner
	go build -o bin/ai-operator-ui ./cmd/ui

.PHONY: ts-types
ts-types: ## Generate the web UI's view types (web/src/generated) from the Go views (tygo.yaml).
	go tool tygo generate

.PHONY: vulncheck
vulncheck: ## Report known vulnerabilities in the Go code paths and dependencies (govulncheck).
	go tool govulncheck ./...

.PHONY: deadcode
# deadcode exits 0 even when it reports something, so any output fails the
# target. Both tools run, so the log lists all findings. -test: code that only
# tests reach counts as live.
deadcode: ## Report unused Go code: staticcheck U1000 (fields, types, constants) and deadcode (unreachable functions).
	@status=0; \
	go tool staticcheck -checks U1000 ./... || status=1; \
	out="$$(go tool deadcode -test ./...)" || status=1; \
	if [ -n "$$out" ]; then echo "$$out"; status=1; fi; \
	exit $$status

.PHONY: gosec
gosec: ## Audit the Go code for insecure patterns (gosec); not run in CI.
	go tool gosec -quiet -exclude-generated ./...

.PHONY: ui
# ts-types first where Go is installed (the CI web job runs in a node image;
# the drift step of the check job checks the generated types).
ui: ## Test, type-check and build the web UI into web/dist/app (embedded by go build), check the bundle budget.
	if command -v go >/dev/null; then $(MAKE) ts-types; fi
	cd web && npm ci && npm test && npm run build && npm run budget

.PHONY: ui-e2e
# The browser's system libraries come with `npx playwright install-deps
# chromium` (root), as in the CI web job.
ui-e2e: ui ## Check the built web UI in Chromium at phone, tablet and desktop width: the full set (docs/contributing/web-ui.md "Checks").
	cd web && npx playwright install chromium && npm run e2e

.PHONY: ui-e2e-journey
ui-e2e-journey: ui ## Only the critical user journeys at 1280 px, the set merge requests run (web/e2e/journey.ts).
	cd web && npx playwright install chromium && npm run e2e:journey

.PHONY: docs-screenshots
docs-screenshots: ui ## Render the Desk screenshots of the docs from the browser checks' fake data into docs/assets/screenshots (needs cwebp; docs/contributing/web-ui.md "Docs screenshots").
	cd web && npx playwright install chromium && npx playwright test -c playwright.docs.config.ts
	mkdir -p docs/assets/screenshots
	for f in web/e2e-results/docs/*.png; do \
		cwebp -quiet -q 80 -m 6 -resize 1920 0 "$$f" -o "docs/assets/screenshots/$$(basename "$$f" .png).webp"; \
	done

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .
	docker build -f Dockerfile.agent -t $(AGENT_IMG) .

.PHONY: install
install: manifests ## Install the CRDs.
	kubectl apply -k config/crd

.PHONY: deploy
deploy: manifests ## Deploy CRDs, RBAC and the operator.
	kubectl apply -k config/default

.PHONY: run
run: ## Run the operator against the current kubeconfig.
	go run ./cmd/operator

.PHONY: run-ui
run-ui: ## Run the web UI without login on http://127.0.0.1:9080 against the current kubeconfig (make ui first; cd web && npm run dev for live reload).
	go run ./cmd/ui --config hack/ui-dev.yaml --bind-address 127.0.0.1:9080 --health-probe-bind-address 127.0.0.1:9081

.PHONY: helm-lint
helm-lint: ## Lint and render the chart.
	$(HELM) lint $(CHART) --strict
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.namespaces={agents} > /dev/null
	$(HELM) lint $(CHART) --strict -f hack/helm-ui-values.yaml
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system -f hack/helm-ui-values.yaml > /dev/null
	$(HELM) lint $(CHART) --strict -f hack/helm-research-values.yaml
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system -f hack/helm-research-values.yaml > /dev/null
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system | grep -q -- '- --agent-language=en$$'
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language="" | grep -q -- '- --agent-language=$$'
	! $(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language="not a tag" > /dev/null 2>&1
	! $(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language=en-12 > /dev/null 2>&1
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language=pt-BR | grep -q -- '- --agent-language=pt-BR$$'
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language=zh-Hant-TW > /dev/null
	$(HELM) template ai-operator $(CHART) --namespace ai-operator-system --set agent.language="not a tag" 2>&1 | grep -q 'agent.language must be'

.PHONY: helm-package
helm-package: ## Package the chart as dist/ai-operator-$(VERSION).tgz.
	$(HELM) package $(CHART) --version $(VERSION) --app-version $(VERSION) -d dist

