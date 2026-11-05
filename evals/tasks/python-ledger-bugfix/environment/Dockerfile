FROM python:3.13-bookworm
WORKDIR /app
# The seed implements every feature with one injected defect; the copy under
# /eval/before lets the verifier measure the change.
COPY app/ /app/
COPY app/ /eval/before/
