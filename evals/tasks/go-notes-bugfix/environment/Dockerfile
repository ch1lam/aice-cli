FROM golang:1.26.5-bookworm
# Python runs the verifier and Harbor's ACP client.
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-venv \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# The seed implements every feature with one injected defect; the copy under
# /eval/before lets the verifier measure the change.
COPY app/ /app/
COPY app/ /eval/before/
