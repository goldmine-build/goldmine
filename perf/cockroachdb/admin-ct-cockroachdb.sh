#!/bin/bash

# Sets up a port-forward to the CockroachDB admin web site and launches
# Chrome.

google-chrome http://localhost:8080
kubectl port-forward perf-ct-cockroachdb-0 8080
