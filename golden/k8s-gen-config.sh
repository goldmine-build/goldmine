#!/bin/bash

print_usage() {
    echo "Usage: $0 INSTANCE_ID"
    echo "       INSTANCE_ID is the id of the instance to be generated."
    exit 1
}
if [ "$#" -ne 1 ]; then
    print_usage
fi

INSTANCE_ID=$1
set -x -e

TMPL_DIR=./k8s-config-templates
INSTANCE_DIR=./k8s-instances

CONF_OUT_DIR="./build"
CONF_MAP="gold-${INSTANCE_ID}-ingestion-config-bt"
# DEPLOY_CONF="${CONF_OUT_DIR}/gold-${INSTANCE_ID}.yaml"
DEPLOY_CONF="${CONF_OUT_DIR}/gold-${INSTANCE_ID}"
INGEST_CONF="${CONF_OUT_DIR}/${CONF_MAP}.json5"

INGESTION_SERVER_CONF="${DEPLOY_CONF}-ingestion-bt.yaml"
CORRECTNESS_CONF="${DEPLOY_CONF}-skiacorrectness.yaml"
BASELINE_SERVER_CONF="${DEPLOY_CONF}-baselineserver.yaml"
DIFF_SERVER_CONF="${DEPLOY_CONF}-diffserver.yaml"

mkdir -p $CONF_OUT_DIR
rm -f $CONF_OUT_DIR/*
rm -f $INGEST_CONF

# Make sure we have the latest and greatest kube-conf-gen
go install ../kube/go/kube-conf-gen

# generate the configuration file for ingestion.
kube-conf-gen -c "${TMPL_DIR}/gold-common.json5" \
              -c "${INSTANCE_DIR}/${INSTANCE_ID}-instance.json5" \
              -extra "INSTANCE_ID:${INSTANCE_ID}" \
              -t "${TMPL_DIR}/ingest-config-template.json5" \
              -parse_conf=false -quote -strict \
              -o "${INGEST_CONF}"

# generate the deployment file for ingestion.
kube-conf-gen -c "${TMPL_DIR}/gold-common.json5" \
              -c "${INSTANCE_DIR}/${INSTANCE_ID}-instance.json5" \
              -extra "INSTANCE_ID:${INSTANCE_ID}" \
              -t "${TMPL_DIR}/gold-ingestion-template-bt.yaml" \
              -parse_conf=false -quote -strict \
              -o "${INGESTION_SERVER_CONF}"

# generate the deployment file for skiacorrectness (the main Gold process)
kube-conf-gen -c "${TMPL_DIR}/gold-common.json5" \
              -c "${INSTANCE_DIR}/${INSTANCE_ID}-instance.json5" \
              -extra "INSTANCE_ID:${INSTANCE_ID}" \
              -t "${TMPL_DIR}/gold-skiacorrectness-template.yaml" \
              -parse_conf=false -quote -strict \
              -o "${CORRECTNESS_CONF}"

# generate the deployment file for the baseline server
kube-conf-gen -c "${TMPL_DIR}/gold-common.json5" \
              -c "${INSTANCE_DIR}/${INSTANCE_ID}-instance.json5" \
              -extra "INSTANCE_ID:${INSTANCE_ID}" \
              -t "${TMPL_DIR}/gold-baselineserver-template.yaml" \
              -parse_conf=false -quote -strict \
              -o "${BASELINE_SERVER_CONF}"

kube-conf-gen -c "${TMPL_DIR}/gold-common.json5" \
              -c "${INSTANCE_DIR}/${INSTANCE_ID}-instance.json5" \
              -extra "INSTANCE_ID:${INSTANCE_ID}" \
              -t "${TMPL_DIR}/gold-diffserver-template.yaml" \
              -parse_conf=false -quote -strict \
              -o "${DIFF_SERVER_CONF}"

set +x

# Push the config map to kubernetes
echo "# To push these run:\n"
echo "kubectl delete configmap $CONF_MAP"
echo "kubectl create configmap $CONF_MAP --from-file=$INGEST_CONF"

# Push the ingestion and show pods so we can see if it landed correctly.
echo "kubectl apply -f ${INGESTION_SERVER_CONF} && kubectl get pods -w -l app=gold-$INSTANCE_ID-ingestion-bt"

# Push the diff server and show pods so we can see if it landed correctly.
echo "kubectl apply -f ${DIFF_SERVER_CONF} && kubectl get pods -w -l app=gold-$INSTANCE_ID-diffserver"

# Push the main server and show pods so we can see if it landed correctly.
echo "kubectl apply -f ${CORRECTNESS_CONF} && kubectl get pods -w -l app=gold-$INSTANCE_ID-skiacorrectness"

# Push the baseline server and show pods so we can see if it landed correctly.
echo "kubectl apply -f ${BASELINE_SERVER_CONF} && kubectl get pods -w -l app=gold-$INSTANCE_ID-baselineserver"

# Push the trace server and show pods so we can see if it landed correctly.
echo "Instance ${INSTANCE_ID} generated."
