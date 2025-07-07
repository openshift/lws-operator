#!/bin/bash
set -o nounset
set -o pipefail

excluded_files=("01_namespace.yaml")
expected_error="delete leaderworkersetoperators.operator.openshift.io cluster"

for file in deploy/*; do
    filename=$(basename "$file")
    if [[ " ${excluded_files[@]} " =~ "$filename" ]]; then
        echo "Skipping excluded file: $file"
        continue
    fi 
    delete_output=$(oc delete -f "$file" 2>&1)
    if [ $? -eq 0 ]; then
        echo "Successfully deleted resources from $file"
    elif [[ $delete_output =~ $expected_error ]]; then
        echo "It is an expected error info: $delete_output"
    else
        echo "[ERROR] Failed to delete resources from $file: $delete_output" >&2
        exit 1
    fi
done
oc delete -f deploy/01_namespace.yaml