IMG ?= ghcr.io/jonathanwiemers/ai-operator:latest
AGENT_IMG ?= ghcr.io/jonathanwiemers/ai-operator-agent:latest
CONTROLLER_GEN ?= $(CURDIR)/bin/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.23.0

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

.PHONY: fmt
fmt:
	gofmt -w api cmd internal

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -race ./...

.PHONY: build
build:
	go build -o bin/manager ./cmd/operator
	go build -o bin/ai-runner ./cmd/runner

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
