CREATE TABLE notification_destinations (
  id uuid PRIMARY KEY,
  name text NOT NULL CHECK (name <> ''),
  endpoint text NOT NULL CHECK (endpoint <> ''),
  allow_http boolean NOT NULL DEFAULT false,
  enabled boolean NOT NULL DEFAULT true,
  events jsonb NOT NULL CHECK (jsonb_typeof(events)='array'),
  secret_ref text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX notification_destinations_name_unique ON notification_destinations(lower(name));

CREATE TABLE notification_deliveries (
  id uuid PRIMARY KEY,
  destination_id uuid NOT NULL REFERENCES notification_destinations(id) ON DELETE RESTRICT,
  audit_event_id uuid NOT NULL REFERENCES audit_events(id) ON DELETE RESTRICT,
  payload jsonb NOT NULL,
  state text NOT NULL CHECK (state IN ('pending','delivered','failed')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  response_status integer,
  last_error text,
  created_at timestamptz NOT NULL,
  delivered_at timestamptz,
  UNIQUE(destination_id,audit_event_id)
);

CREATE OR REPLACE FUNCTION queue_notification_deliveries() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  destination record;
  delivery_id uuid;
  delivery_payload jsonb;
BEGIN
  delivery_payload := jsonb_build_object(
    'event_id', NEW.id,
    'action', NEW.action,
    'aggregate_type', NEW.aggregate_type,
    'aggregate_id', NEW.aggregate_id,
    'occurred_at', NEW.occurred_at
  );
  FOR destination IN
    SELECT id FROM notification_destinations
    WHERE enabled AND (events ? NEW.action OR events ? '*')
  LOOP
    delivery_id := gen_random_uuid();
    INSERT INTO notification_deliveries(id,destination_id,audit_event_id,payload,state,created_at)
      VALUES(delivery_id,destination.id,NEW.id,delivery_payload,'pending',NEW.occurred_at)
      ON CONFLICT(destination_id,audit_event_id) DO NOTHING;
    IF FOUND THEN
      INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
        VALUES(gen_random_uuid(),'notification_delivery',delivery_id,1,'notification.deliver_requested',jsonb_build_object('delivery_id',delivery_id),NEW.occurred_at,NEW.occurred_at)
        ON CONFLICT DO NOTHING;
    END IF;
  END LOOP;
  RETURN NEW;
END $$;

CREATE TRIGGER audit_event_notifications
AFTER INSERT ON audit_events
FOR EACH ROW EXECUTE FUNCTION queue_notification_deliveries();
