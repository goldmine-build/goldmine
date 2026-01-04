# Keep the tag for base-cipd in sync with the tag used here for debian.
FROM debian:testing-slim AS base

# procps openjdk-21-jre python3 - needed for goldmine tests.
RUN apt-get update && apt-get upgrade -y && apt-get install -y  \
  adduser ca-certificates git golang procps openjdk-21-jre python3 \
  && rm -rf /var/lib/apt/lists/* \
  && addgroup --gid 2000 goldminer \
  && adduser --uid 2000 --gid 2000 goldminer
RUN mkdir -p /home/goldminer/bin
ENV GOPATH=/home/goldminer
ENV PATH="$PATH:$GOPATH/bin"
RUN go install github.com/bazelbuild/bazelisk@latest

# Needed for git unit tests.
RUN git config --global user.email "you@example.com"
RUN git config --global user.name "Your Name"
# USER goldminer:goldminer
