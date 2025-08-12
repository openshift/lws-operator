for OCP_VERSION in v4.18 v4.19 v4.20; do
    # ref: https://github.com/konflux-ci/olm-operator-konflux-sample/blob/main/docs/konflux-onboarding.md#building-a-file-based-catalog
    /home/ardaguclu/Downloads/linux-amd64-opm alpha render-template semver $OCP_VERSION/catalog-template.yaml --migrate-level=bundle-object-to-csv-metadata > $OCP_VERSION/catalog/lws-operator/catalog.json;
done
