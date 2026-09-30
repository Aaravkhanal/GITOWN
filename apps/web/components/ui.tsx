"use client";

import { useEffect, useState } from "react";
import {
  AlertCircle,
  Check,
  Copy,
  LoaderCircle,
  Plus,
  RefreshCw,
  Trash2,
  Webhook as WebhookIcon,
} from "lucide-react";
import {
  api,
  date,
  patch,
  post,
  remove,
  type Webhook,
  type WebhookDelivery,
  type WebhookEvent,
} from "@/lib/api";

export function useData<T>(path: string | null, version = 0) {
  const [state, setState] = useState<{
    path?: string | null;
    data?: T;
    error?: string;
    loading: boolean;
  }>({ path, loading: !!path });
  useEffect(() => {
    let current = true;
    if (!path) {
      setState({ path, loading: false });
      return;
    }
    // Refreshing the same resource keeps the current data on screen so
    // saving a form does not unmount the page behind a loading spinner.
    setState((previous) =>
      previous.path === path && previous.data !== undefined
        ? previous
        : { path, loading: true },
    );
    api<T>(path)
      .then((data) => {
        if (current) setState({ path, data, loading: false });
      })
      .catch((error) => {
        if (current) setState({ path, error: error.message, loading: false });
      });
    return () => {
      current = false;
    };
  }, [path, version]);
  return state;
}
export function ErrorMessage({ error }: { error?: string }) {
  return error ? (
    <div className="error" role="alert">
      <AlertCircle size={17} />
      {error}
    </div>
  ) : null;
}
export function Loading() {
  return (
    <div className="loading" role="status">
      <LoaderCircle className="spin" size={20} /> Loading your workspace…
    </div>
  );
}
export function Avatar({
  name,
  small = false,
}: {
  name: string;
  small?: boolean;
}) {
  return (
    <span className={`avatar ${small ? "small" : ""}`} aria-hidden="true">
      {name.slice(0, 2).toUpperCase()}
    </span>
  );
}
export function Badge({
  children,
  kind = "",
}: {
  children: React.ReactNode;
  kind?: string;
}) {
  return <span className={`badge ${kind}`}>{children}</span>;
}
export function CopyButton({
  text,
  label = "Copy",
}: {
  text: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState(false);
  useEffect(() => {
    if (copied) {
      const timer = setTimeout(() => setCopied(false), 2000);
      return () => clearTimeout(timer);
    }
  }, [copied]);
  return (
    <button
      className="copy-button"
      aria-label={
        error ? "Copy failed; select text manually" : copied ? "Copied" : label
      }
      title={error ? "Select and copy the text manually" : label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setError(false);
        } catch {
          setError(true);
        }
      }}
    >
      {copied ? <Check size={16} /> : <Copy size={16} />}
      {copied && <span>Copied</span>}
      {error && <span>Copy failed</span>}
    </button>
  );
}

const WEBHOOK_EVENTS: WebhookEvent[] = [
  "push",
  "issue.opened",
  "issue.closed",
  "issue.reopened",
  "issue.commented",
  "pull.opened",
  "pull.closed",
  "pull.reopened",
  "pull.merged",
  "pull.reviewed",
  "pull.commented",
  "drop.published",
];

// WebhookSettings manages signed webhook subscriptions for either a
// repository or a district — endpoint is whichever base path the caller's
// server-side scope check already resolved to (e.g. /repos/{owner}/{repo}
// or /districts/{slug}), so this one component serves both.
export function WebhookSettings({
  endpoint,
  description,
}: {
  endpoint: string;
  description?: string;
}) {
  const [version, setVersion] = useState(0);
  const hooks = useData<{ items: Webhook[] }>(`${endpoint}/webhooks`, version);
  const [error, setError] = useState("");
  const [newSecret, setNewSecret] = useState<{
    id: string;
    secret: string;
  } | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const refresh = () => setVersion((v) => v + 1);
  return (
    <div className="panel webhook-settings">
      <div className="section-heading">
        <div>
          <h2>
            <WebhookIcon size={18} /> Webhooks
          </h2>
          <p>
            {description ||
              "Send a signed HTTP POST to another service whenever something happens here. Verify the X-GITOWN-Signature header against the secret shown when a webhook is created."}
          </p>
        </div>
      </div>
      <ErrorMessage error={error || hooks.error} />
      {newSecret && (
        <div className="panel success-box">
          <strong>Save this secret now — it will not be shown again.</strong>
          <pre>
            <code>{newSecret.secret}</code>
          </pre>
        </div>
      )}
      <form
        className="webhook-form"
        onSubmit={async (event) => {
          event.preventDefault();
          setError("");
          setNewSecret(null);
          const data = new FormData(event.currentTarget);
          const events = WEBHOOK_EVENTS.filter(
            (kind) => data.get(`event_${kind}`) === "on",
          );
          if (events.length === 0) {
            setError("Choose at least one event to subscribe to.");
            return;
          }
          try {
            const created = await post<Webhook>(`${endpoint}/webhooks`, {
              url: String(data.get("url") || ""),
              events,
            });
            if (created.secret) {
              setNewSecret({ id: created.id, secret: created.secret });
            }
            refresh();
            event.currentTarget.reset();
          } catch (createError) {
            setError((createError as Error).message);
          }
        }}
      >
        <label>
          URL
          <input
            name="url"
            type="url"
            required
            placeholder="https://example.com/hook"
          />
        </label>
        <fieldset className="webhook-events">
          <legend>Events</legend>
          {WEBHOOK_EVENTS.map((kind) => (
            <label key={kind} className="checkbox-row">
              <input name={`event_${kind}`} type="checkbox" />
              {kind}
            </label>
          ))}
        </fieldset>
        <button className="button primary" type="submit">
          <Plus size={16} /> Add webhook
        </button>
      </form>
      {hooks.loading ? (
        <Loading />
      ) : hooks.data?.items.length ? (
        <div className="webhook-list">
          {hooks.data.items.map((hook) => (
            <WebhookRow
              key={hook.id}
              endpoint={endpoint}
              hook={hook}
              expanded={expanded === hook.id}
              onToggle={() =>
                setExpanded(expanded === hook.id ? null : hook.id)
              }
              onChanged={refresh}
              onError={setError}
            />
          ))}
        </div>
      ) : (
        <div className="empty-inline">No webhooks yet.</div>
      )}
    </div>
  );
}

function WebhookRow({
  endpoint,
  hook,
  expanded,
  onToggle,
  onChanged,
  onError,
}: {
  endpoint: string;
  hook: Webhook;
  expanded: boolean;
  onToggle: () => void;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const [deliveryVersion, setDeliveryVersion] = useState(0);
  const deliveries = useData<{ items: WebhookDelivery[] }>(
    expanded ? `${endpoint}/webhooks/${hook.id}/deliveries` : null,
    deliveryVersion,
  );
  return (
    <div className="webhook-row-group">
      <div className="webhook-row">
        <div>
          <strong>{hook.url}</strong>
          <span>
            {hook.events.join(", ")} · {hook.active ? "Active" : "Paused"}
          </span>
        </div>
        <button
          type="button"
          className="button small-button"
          onClick={onToggle}
        >
          {expanded ? "Hide deliveries" : "Deliveries"}
        </button>
        <button
          type="button"
          className="button small-button"
          onClick={async () => {
            try {
              await patch(`${endpoint}/webhooks/${hook.id}`, {
                active: !hook.active,
              });
              onChanged();
            } catch (toggleError) {
              onError((toggleError as Error).message);
            }
          }}
        >
          {hook.active ? "Pause" : "Resume"}
        </button>
        <button
          aria-label={`Remove webhook to ${hook.url}`}
          className="icon-button danger-icon"
          type="button"
          onClick={async () => {
            try {
              await remove(`${endpoint}/webhooks/${hook.id}`);
              onChanged();
            } catch (removeError) {
              onError((removeError as Error).message);
            }
          }}
        >
          <Trash2 size={16} />
        </button>
      </div>
      {expanded && (
        <div className="webhook-deliveries">
          {deliveries.loading ? (
            <Loading />
          ) : deliveries.data?.items.length ? (
            deliveries.data.items.map((delivery) => (
              <div className="webhook-delivery-row" key={delivery.id}>
                <span>{delivery.event}</span>
                <Badge
                  kind={
                    delivery.status === "success"
                      ? "green"
                      : delivery.status === "failed"
                        ? "red"
                        : ""
                  }
                >
                  {delivery.status}
                </Badge>
                <span>{delivery.response_status ?? "—"}</span>
                <span className="muted small-text">
                  {date(delivery.created_at)}
                </span>
                <button
                  type="button"
                  className="icon-button"
                  aria-label={`Replay delivery of ${delivery.event}`}
                  onClick={async () => {
                    try {
                      await post(
                        `${endpoint}/webhooks/${hook.id}/deliveries/${delivery.id}/replay`,
                        {},
                      );
                      setDeliveryVersion((v) => v + 1);
                    } catch (replayError) {
                      onError((replayError as Error).message);
                    }
                  }}
                >
                  <RefreshCw size={14} />
                </button>
              </div>
            ))
          ) : (
            <div className="empty-inline">No deliveries yet.</div>
          )}
        </div>
      )}
    </div>
  );
}
