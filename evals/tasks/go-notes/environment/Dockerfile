FROM golang:1.26.5-bookworm
# Python runs the verifier and Harbor's ACP client.
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-venv \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY go.mod /app/go.mod
