set shell := ["bash", "-euo", "pipefail", "-c"]

version := env_var_or_default("VERSION", "dev-build")
app_version := env_var_or_default("APP_VERSION", "2.23.2-bwhotreload.3")
commit := env_var_or_default("COMMIT", "none")
date := env_var_or_default("DATE", "unknown")
repository := env_var_or_default("REPOSITORY", "antoniomika/sish")
image := env_var_or_default("IMAGE", "fabiop85/sish:dev")

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
