LOCK TABLE dataset_manifest_payload_bindings,dataset_market_payloads,dataset_manifest_observations,dataset_manifest_partitions,dataset_manifests IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM dataset_manifest_payload_bindings) OR
       EXISTS(SELECT 1 FROM dataset_market_payloads) THEN
        RAISE EXCEPTION 'cannot roll back migration 110 while immutable market payload evidence exists';
    END IF;
END;
$$;

DROP TRIGGER trg_dataset_payload_binding_graph ON dataset_manifest_payload_bindings;
DROP TRIGGER trg_dataset_manifest_payload_bindings_immutable ON dataset_manifest_payload_bindings;
DROP TRIGGER trg_dataset_market_payloads_immutable ON dataset_market_payloads;
DROP FUNCTION validate_dataset_payload_binding();
DROP FUNCTION reject_dataset_payload_mutation();
DROP TABLE dataset_manifest_payload_bindings;
DROP TABLE dataset_market_payloads;
