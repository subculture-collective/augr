LOCK TABLE instruments,dataset_manifests,dataset_manifest_partitions,dataset_manifest_observations IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE dataset_market_payloads (
    id UUID PRIMARY KEY,
    schema_name TEXT NOT NULL CHECK (schema_name='dataset-market-payload-v1'),
    payload_kind TEXT NOT NULL CHECK (payload_kind IN ('stock_bar','option_bar','option_quote','option_trade','option_contract','option_snapshot')),
    instrument_id UUID NOT NULL REFERENCES instruments(id) ON DELETE RESTRICT,
    underlying_instrument_id UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider<>'' AND provider=btrim(provider)),
    feed TEXT NOT NULL CHECK (feed<>'' AND feed=btrim(feed)),
    symbol TEXT NOT NULL CHECK (symbol<>'' AND symbol=btrim(symbol)),
    underlying_symbol TEXT NOT NULL CHECK (underlying_symbol=btrim(underlying_symbol)),
    timeframe TEXT NOT NULL CHECK (timeframe<>'' AND timeframe=btrim(timeframe)),
    adjustment_policy TEXT NOT NULL CHECK (adjustment_policy<>'' AND adjustment_policy=btrim(adjustment_policy)),
    effective_at TIMESTAMPTZ NOT NULL CHECK (effective_at=date_trunc('microseconds',effective_at)),
    published_at TIMESTAMPTZ CHECK (published_at IS NULL OR published_at=date_trunc('microseconds',published_at)),
    observed_at TIMESTAMPTZ NOT NULL CHECK (observed_at=date_trunc('microseconds',observed_at)),
    available_at TIMESTAMPTZ NOT NULL CHECK (available_at=date_trunc('microseconds',available_at)),
    revision TEXT NOT NULL CHECK (revision=btrim(revision)),
    correction_of_sha256 TEXT NOT NULL CHECK (correction_of_sha256='' OR correction_of_sha256 ~ '^[0-9a-f]{64}$'),
    content_sha256 TEXT NOT NULL UNIQUE CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    canonical_bytes BYTEA NOT NULL,
    canonical_json JSONB NOT NULL CHECK (jsonb_typeof(canonical_json)='object'),
    created_at TIMESTAMPTZ NOT NULL CHECK (created_at=date_trunc('microseconds',created_at)),
    CHECK (published_at IS NULL OR published_at<=observed_at),
    CHECK (observed_at<=available_at),
    CHECK ((payload_kind='stock_bar' AND underlying_instrument_id IS NULL AND underlying_symbol='') OR
           (payload_kind<>'stock_bar' AND underlying_instrument_id IS NOT NULL AND underlying_symbol<>'')),
    CHECK (content_sha256=encode(digest(canonical_bytes,'sha256'),'hex')),
    CHECK (canonical_json=convert_from(canonical_bytes,'UTF8')::JSONB),
    CHECK (canonical_json->>'schema'=schema_name),
    CHECK (canonical_json->>'kind'=payload_kind),
    CHECK ((canonical_json->>'instrument_id')::UUID=instrument_id),
    CHECK (NULLIF(canonical_json->>'underlying_instrument_id','')::UUID IS NOT DISTINCT FROM underlying_instrument_id),
    CHECK (canonical_json->>'provider'=provider AND canonical_json->>'feed'=feed AND canonical_json->>'symbol'=symbol),
    CHECK (canonical_json->>'underlying_symbol'=underlying_symbol AND canonical_json->>'timeframe'=timeframe),
    CHECK (canonical_json->>'adjustment_policy'=adjustment_policy AND canonical_json->>'revision'=revision),
    CHECK (canonical_json->>'correction_of_sha256'=correction_of_sha256),
    CHECK (canonical_json->>'effective_at'=to_char(effective_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')),
    CHECK (canonical_json->>'observed_at'=to_char(observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')),
    CHECK (canonical_json->>'available_at'=to_char(available_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')),
    CHECK (canonical_json->>'published_at'=CASE WHEN published_at IS NULL THEN '' ELSE to_char(published_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END),
    CHECK ((payload_kind IN ('stock_bar','option_bar'))=(canonical_json->'bar' IS DISTINCT FROM 'null'::JSONB)),
    CHECK ((payload_kind='option_quote')=(canonical_json->'quote' IS DISTINCT FROM 'null'::JSONB)),
    CHECK ((payload_kind='option_trade')=(canonical_json->'trade' IS DISTINCT FROM 'null'::JSONB)),
    CHECK ((payload_kind='option_contract')=(canonical_json->'contract' IS DISTINCT FROM 'null'::JSONB)),
    CHECK ((payload_kind='option_snapshot')=(canonical_json->'snapshot' IS DISTINCT FROM 'null'::JSONB)),
    CHECK (id=economic_deterministic_uuid('dataset-market-payload',schema_name || '@sha256:' || content_sha256))
);

CREATE TABLE dataset_manifest_payload_bindings (
    manifest_id UUID NOT NULL,
    partition_sequence INTEGER NOT NULL,
    observation_sequence INTEGER NOT NULL,
    payload_id UUID NOT NULL REFERENCES dataset_market_payloads(id) ON DELETE RESTRICT,
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL CHECK (created_at=date_trunc('microseconds',created_at)),
    PRIMARY KEY(manifest_id,partition_sequence,observation_sequence),
    FOREIGN KEY(manifest_id,partition_sequence,observation_sequence)
        REFERENCES dataset_manifest_observations(manifest_id,partition_sequence,sequence) ON DELETE RESTRICT,
    FOREIGN KEY(content_sha256) REFERENCES dataset_market_payloads(content_sha256) ON DELETE RESTRICT,
    UNIQUE(manifest_id,partition_sequence,payload_id)
);

CREATE FUNCTION validate_dataset_payload_binding() RETURNS TRIGGER AS $$
DECLARE
    observation RECORD;
    payload dataset_market_payloads%ROWTYPE;
BEGIN
    SELECT obs.content_sha256,obs.instrument_id,obs.effective_at,
           obs.published_at,obs.observed_at,obs.available_at,
           obs.revision,obs.correction_of,part.kind,part.provider,
           part.adjustment_policy
      INTO observation
      FROM dataset_manifest_observations obs
      JOIN dataset_manifest_partitions part
        ON part.manifest_id=obs.manifest_id
       AND part.sequence=obs.partition_sequence
     WHERE obs.manifest_id=NEW.manifest_id
       AND obs.partition_sequence=NEW.partition_sequence
       AND obs.sequence=NEW.observation_sequence;
    SELECT * INTO payload FROM dataset_market_payloads WHERE id=NEW.payload_id;
    IF observation.content_sha256 IS NULL OR payload.id IS NULL OR
       NEW.content_sha256<>observation.content_sha256 OR
       NEW.content_sha256<>payload.content_sha256 OR
       observation.instrument_id IS DISTINCT FROM payload.instrument_id OR
       observation.effective_at<>payload.effective_at OR
       observation.published_at IS DISTINCT FROM payload.published_at OR
       observation.observed_at<>payload.observed_at OR
       observation.available_at<>payload.available_at OR
       observation.revision<>payload.revision OR
       observation.correction_of<>payload.correction_of_sha256 OR
       observation.provider<>payload.provider OR
       observation.adjustment_policy<>payload.adjustment_policy OR
       observation.kind NOT IN ('bars','quotes','option_contracts','option_chains','external_object') OR
       (observation.kind='bars')<>(payload.payload_kind IN ('stock_bar','option_bar')) OR
       (observation.kind='quotes')<>(payload.payload_kind='option_quote') OR
       (observation.kind='option_contracts')<>(payload.payload_kind='option_contract') OR
       (observation.kind='option_chains')<>(payload.payload_kind='option_snapshot') OR
       (observation.kind='external_object')<>(payload.payload_kind='option_trade') OR
       payload.available_at>(SELECT decision_cutoff FROM dataset_manifests WHERE id=NEW.manifest_id) THEN
        RAISE EXCEPTION 'dataset payload binding does not reconstruct';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION reject_dataset_payload_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'dataset market payload evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_dataset_market_payloads_immutable
BEFORE UPDATE OR DELETE ON dataset_market_payloads
FOR EACH ROW EXECUTE FUNCTION reject_dataset_payload_mutation();

CREATE TRIGGER trg_dataset_manifest_payload_bindings_immutable
BEFORE UPDATE OR DELETE ON dataset_manifest_payload_bindings
FOR EACH ROW EXECUTE FUNCTION reject_dataset_payload_mutation();

CREATE CONSTRAINT TRIGGER trg_dataset_payload_binding_graph
AFTER INSERT ON dataset_manifest_payload_bindings
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_dataset_payload_binding();

CREATE INDEX idx_dataset_market_payload_instrument_time
    ON dataset_market_payloads(instrument_id,payload_kind,timeframe,effective_at,available_at,id);
CREATE INDEX idx_dataset_market_payload_underlying_time
    ON dataset_market_payloads(underlying_instrument_id,payload_kind,effective_at,available_at,id)
    WHERE underlying_instrument_id IS NOT NULL;
CREATE INDEX idx_dataset_manifest_payload_bindings_payload
    ON dataset_manifest_payload_bindings(payload_id,manifest_id,partition_sequence,observation_sequence);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='augr_app_runtime') THEN
        GRANT SELECT ON dataset_market_payloads,dataset_manifest_payload_bindings TO augr_app_runtime;
    END IF;
END;
$$;
