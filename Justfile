set shell := ["bash", "-euo", "pipefail", "-c"]

version := env_var_or_default("VERSION", "dev-build")
app_version := env_var_or_default("APP_VERSION", "2.23.2-newcleanup.2")
commit := env_var_or_default("COMMIT", "none")
date := env_var_or_default("DATE", "unknown")
repository := env_var_or_default("REPOSITORY", "antoniomika/sish")
image := env_var_or_default("IMAGE", "fabiop85/sish:newcleanup")

docker-build app_version=app_version commit=commit date=date repository=repository image=image:
	docker build \
	    --no-cache \
		--target release \
		--build-arg VERSION={{app_version}} \
		--build-arg COMMIT={{commit}} \
		--build-arg DATE={{date}} \
		--build-arg REPOSITORY={{repository}} \
		-t {{image}} .
	docker push {{image}}

# Manual validation profile (safe for workstation):
# - bounded CPU/RAM
# - serial package execution (-p 1)
# - no heavy stress tests by default
test-manual cpus="2" mem="2GiB" pkgs="./sshmuxer ./utils" run="TestWithForceConnectTargetLock|TestForwardLifecycle|TestDirty|TestLifecycleMetrics|TestBuildDirtySnapshot" count="1":
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test {{pkgs}} -run '{{run}}' -count={{count}} -p 1

# Manual race validation with bounded resources.
test-manual-race cpus="2" mem="2GiB" pkgs="./sshmuxer" run="TestWithForceConnectTargetLock|TestForwardLifecycle" count="1":
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test -race {{pkgs}} -run '{{run}}' -count={{count}} -p 1

# Opt-in stress validation (disabled by default in tests; enabled here explicitly).
test-manual-stress cpus="2" mem="2GiB" pkgs="./utils" run="TestStress" count="1":
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_STRESS_TESTS=1 SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test {{pkgs}} -run '{{run}}' -count={{count}} -p 1

# Full safe stage pipeline:
# 1) targeted functional tests
# 2) targeted race tests
# 3) repo-wide smoke compile/run (non-race, no stress)
test-full-safe-stage cpus="2" mem="2GiB":
	@echo "==> [1/3] targeted functional tests"
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test ./sshmuxer ./utils -run 'TestWithForceConnectTargetLock|TestForwardLifecycle|TestDirty|TestLifecycleMetrics|TestBuildDirtySnapshot' -count=1 -p 1
	@echo "==> [2/3] targeted race tests"
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test -race ./sshmuxer -run 'TestWithForceConnectTargetLock|TestForwardLifecycle' -count=1 -p 1
	@echo "==> [3/3] smoke tests (all packages, non-race)"
	CPUS="{{cpus}}"; CPUS="${CPUS#*=}"; \
	MEM="{{mem}}"; MEM="${MEM#*=}"; \
	GOMAXPROCS="${CPUS}" GOMEMLIMIT="${MEM}" SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
		go test ./... -count=1 -p 1
