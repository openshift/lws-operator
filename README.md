### FBC generation

Each OCP version we support will have a dir under fbc/, i.e. fbc/v4.18.  The directories will include:
- a container file for the fbc image
- a catalog template file
- a catalog/lws-operator dir with the actual fbc fragment

On release of a new LWS Operator, the template files for each supported OCP version will need additional entries for the new images in the appropriate channels.  Then the fragement will need to be regenerated with the following command:

$ ./generate-fbc.sh

NOTE: Starting with OCP 4.17 you need the --migrate-level=bundle-object-to-csv-metadata flag. For rendering to older versions of OCP, simply omit the flag.

Note: You need opm version 1.47.0 or higher

You can read more at [Konflux example repo](https://github.com/konflux-ci/olm-operator-konflux-sample/blob/main/docs/konflux-onboarding.md#building-a-file-based-catalog)
