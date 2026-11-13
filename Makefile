IMG ?= ghcr.io/jonathanwiemers/ai-operator:latest
AGENT_IMG ?= ghcr.io/jonathanwiemers/ai-operator-agent:latest
CONTROLLER_GEN ?= $(CURDIR)/bin/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.23.0
HELM ?= helm
CHART := charts/ai-operator
# Chart version for helm-package. Releases pass the tag (YEAR.MONTH.N, see
# docs/releasing.md); the operator picks it, never the build.
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
	go test -race ./...

.PHONY: test-db
test-db: ## Start a Postgres for the research store tests (docker, 127.0.0.1:55432).
	docker run -d --rm --name ai-operator-test-db -p 127.0.0.1:55432:5432 \
		-e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ai_operator_test postgres:17 > /dev/null
	@echo 'export AI_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/ai_operator_test?sslmode=disable'

.PHONY: research-seed
research-seed: ## Migrate and seed a research database on the test-db Postgres for make run-ui (see docs/ui.md "Development").
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

.PHONY: ui
# ts-types first where Go is installed (the CI ui job runs in a node image;
# the drift job checks the generated types).
ui: ## Test, type-check and build the web UI into web/dist/app (embedded by go build), check the bundle budget.
	if command -v go >/dev/null; then $(MAKE) ts-types; fi
	cd web && npm ci && npm test && npm run build && npm run budget

.PHONY: ui-e2e
# The browser's system libraries come with `npx playwright install-deps
# chromium` (root), as in the CI ui-e2e job.
ui-e2e: ui ## Check the built web UI in Chromium at phone, tablet and desktop width (docs/ui.md "Checks").
	cd web && npx playwright install chromium && npm run e2e

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

.PHONY: helm-package
helm-package: ## Package the chart as dist/ai-operator-$(VERSION).tgz.
	$(HELM) package $(CHART) --version $(VERSION) --app-version $(VERSION) -d dist

