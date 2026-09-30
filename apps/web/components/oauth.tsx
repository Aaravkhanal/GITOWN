"use client";

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Bot,
  ChevronRight,
  KeyRound,
  Plug,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  X,
} from "lucide-react";
import {
  date,
  post,
  remove,
  type AuthorizedApp,
  type GitownApp,
  type OAuthApp,
} from "@/lib/api";
import { CopyButton, ErrorMessage, Loading, useData } from "./ui";

// DeveloperAppsPage covers everything a developer needs to register and
// manage integrations, and everything an end user needs to see and revoke
// what they have authorized — three self-contained sections on one page,
// matching how other settings pages in this codebase group related
// concerns rather than splitting into many single-purpose routes.
export function DeveloperAppsPage() {
  return (
    <div className="form-page wide-form">
      <div className="breadcrumb">
        <span>Settings</span>
        <ChevronRight size={12} />
        Apps and integrations
      </div>
      <div className="page-heading">
        <div>
          <h1>Apps and integrations.</h1>
          <p>
            Register an OAuth application for the standard authorization flow,
            or a GITOWN App that installs directly onto a repository.
          </p>
        </div>
        <Plug size={30} className="muted" />
      </div>
      <OAuthAppsSection />
      <GitownAppsSection />
      <AuthorizedAppsSection />
    </div>
  );
}

function OAuthAppsSection() {
  const [version, setVersion] = useState(0);
  const apps = useData<{ items: OAuthApp[] }>("/user/oauth-apps", version);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [revealed, setRevealed] = useState<{
    id: string;
    secret: string;
  } | null>(null);
  const refresh = () => setVersion((v) => v + 1);
  return (
    <section className="panel form-panel" aria-label="OAuth applications">
      <h2>
        <KeyRound size={18} /> OAuth applications
      </h2>
      <p className="muted">
        A user signs into your app and approves the scope it asks for. Your app
        never sees their password.
      </p>
      <ErrorMessage error={error || apps.error} />
      {revealed && (
        <div className="secret-box">
          <div>
            <ShieldCheck size={18} />
            <strong>Client secret ready. Copy it now.</strong>
            <button
              className="icon-button"
              onClick={() => setRevealed(null)}
              aria-label="Dismiss secret"
            >
              <X size={17} />
            </button>
          </div>
          <p>This is the only time the full secret is shown.</p>
          <div className="copy-field">
            <code>{revealed.secret}</code>
            <CopyButton text={revealed.secret} />
          </div>
        </div>
      )}
      <form
        className="two-fields"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          const form = event.currentTarget;
          const data = new FormData(form);
          try {
            const created = await post<OAuthApp>("/user/oauth-apps", {
              name: data.get("name"),
              description: data.get("description") || "",
              homepage_url: data.get("homepage_url") || "",
              redirect_uri: data.get("redirect_uri"),
            });
            if (created.client_secret) {
              setRevealed({ id: created.id, secret: created.client_secret });
            }
            refresh();
            form.reset();
          } catch (createError) {
            setError((createError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Name
          <input name="name" maxLength={80} required />
        </label>
        <label>
          Redirect URI
          <input
            name="redirect_uri"
            type="url"
            placeholder="https://example.com/callback"
            required
          />
        </label>
        <label>
          Homepage
          <input
            name="homepage_url"
            type="url"
            placeholder="https://example.com"
          />
        </label>
        <label>
          Description
          <input name="description" maxLength={500} />
        </label>
        <button className="button primary" disabled={busy} type="submit">
          <Plus size={15} /> Register OAuth app
        </button>
      </form>
      {apps.loading ? (
        <Loading />
      ) : apps.data?.items.length ? (
        <div className="token-list">
          {apps.data.items.map((app) => (
            <div className="token-row" key={app.id}>
              <KeyRound size={19} />
              <div>
                <strong>{app.name}</strong>
                <span>
                  client_id {app.client_id} · {app.authorized_users} user
                  {app.authorized_users === 1 ? "" : "s"} authorized
                </span>
              </div>
              <button
                className="button small-button"
                disabled={busy}
                type="button"
                onClick={async () => {
                  setBusy(true);
                  setError("");
                  try {
                    const result = await post<{ client_secret: string }>(
                      `/user/oauth-apps/${app.id}/secret`,
                      {},
                    );
                    setRevealed({ id: app.id, secret: result.client_secret });
                  } catch (regenError) {
                    setError((regenError as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <RefreshCw size={14} /> New secret
              </button>
              <button
                aria-label={`Delete ${app.name}`}
                className="icon-button danger-icon"
                disabled={busy}
                type="button"
                onClick={async () => {
                  if (
                    !window.confirm(
                      `Delete “${app.name}”? Every token it issued stops working.`,
                    )
                  )
                    return;
                  setBusy(true);
                  setError("");
                  try {
                    await remove(`/user/oauth-apps/${app.id}`);
                    refresh();
                  } catch (deleteError) {
                    setError((deleteError as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-inline">No OAuth applications yet.</div>
      )}
    </section>
  );
}

function GitownAppsSection() {
  const [version, setVersion] = useState(0);
  const apps = useData<{ items: GitownApp[] }>("/user/gitown-apps", version);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = () => setVersion((v) => v + 1);
  return (
    <section className="panel form-panel" aria-label="GITOWN Apps">
      <h2>
        <Bot size={18} /> GITOWN Apps
      </h2>
      <p className="muted">
        A repository owner installs your app directly, granting it access to
        only that repository — no user has to sign in first. Each app gets its
        own bot identity, shown below as its username. Installation tokens here
        are long-lived rather than short-lived and auto-refreshed, a deliberate
        simplification from a full app-identity model.
      </p>
      <ErrorMessage error={error || apps.error} />
      <form
        className="two-fields"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          const form = event.currentTarget;
          const data = new FormData(form);
          try {
            await post("/user/gitown-apps", {
              name: data.get("name"),
              description: data.get("description") || "",
              homepage_url: data.get("homepage_url") || "",
              webhook_url: data.get("webhook_url") || "",
              requested_scope: data.get("requested_scope"),
            });
            refresh();
            form.reset();
          } catch (createError) {
            setError((createError as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Name
          <input name="name" maxLength={80} required />
        </label>
        <label>
          Requested access
          <select name="requested_scope" defaultValue="repo:read">
            <option value="repo:read">Read only</option>
            <option value="repo:write">Read and write</option>
          </select>
        </label>
        <label>
          Homepage
          <input
            name="homepage_url"
            type="url"
            placeholder="https://example.com"
          />
        </label>
        <label>
          Webhook URL (optional)
          <input
            name="webhook_url"
            type="url"
            placeholder="https://example.com/gitown-events"
          />
        </label>
        <label className="full-width">
          Description
          <input name="description" maxLength={500} />
        </label>
        <button className="button primary" disabled={busy} type="submit">
          <Plus size={15} /> Register GITOWN App
        </button>
      </form>
      {apps.loading ? (
        <Loading />
      ) : apps.data?.items.length ? (
        <div className="token-list">
          {apps.data.items.map((app) => (
            <div className="token-row" key={app.id}>
              <Bot size={19} />
              <div>
                <strong>{app.name}</strong>
                <span>
                  @{app.bot_username} · requests {app.requested_scope} ·{" "}
                  {app.installations} installation
                  {app.installations === 1 ? "" : "s"}
                </span>
              </div>
              <button
                aria-label={`Delete ${app.name}`}
                className="icon-button danger-icon"
                disabled={busy}
                type="button"
                onClick={async () => {
                  if (
                    !window.confirm(
                      `Delete “${app.name}”? Every installation and token it holds stops working.`,
                    )
                  )
                    return;
                  setBusy(true);
                  setError("");
                  try {
                    await remove(`/user/gitown-apps/${app.id}`);
                    refresh();
                  } catch (deleteError) {
                    setError((deleteError as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-inline">No GITOWN Apps yet.</div>
      )}
    </section>
  );
}

function AuthorizedAppsSection() {
  const [version, setVersion] = useState(0);
  const apps = useData<{ items: AuthorizedApp[] }>(
    "/user/authorized-apps",
    version,
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = () => setVersion((v) => v + 1);
  return (
    <section className="panel form-panel" aria-label="Authorized applications">
      <h2>Applications you have authorized</h2>
      <p className="muted">
        These OAuth apps can act on your behalf within the scope you approved.
        Revoking stops that immediately.
      </p>
      <ErrorMessage error={error || apps.error} />
      {apps.loading ? (
        <Loading />
      ) : apps.data?.items.length ? (
        <div className="token-list">
          {apps.data.items.map((entry) => (
            <div className="token-row" key={entry.oauth_app_id}>
              <KeyRound size={19} />
              <div>
                <strong>{entry.name}</strong>
                <span>
                  {entry.scope} · Authorized {date(entry.created_at)}
                </span>
              </div>
              <button
                className="button danger small-button"
                disabled={busy}
                type="button"
                onClick={async () => {
                  if (!window.confirm(`Revoke access for “${entry.name}”?`))
                    return;
                  setBusy(true);
                  setError("");
                  try {
                    await remove(`/user/authorized-apps/${entry.oauth_app_id}`);
                    refresh();
                  } catch (revokeError) {
                    setError((revokeError as Error).message);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Revoke
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty-inline">No authorized applications.</div>
      )}
    </section>
  );
}

// OAuthAuthorizePage is the consent screen a third-party app's browser
// redirect lands a signed-in user on. It never issues a bare 3xx itself:
// approving/denying asks the API for the exact redirect to send the
// browser to next, then navigates there directly (the target is an
// external site, so this is a real navigation, not client-side routing).
export function OAuthAuthorizePage() {
  const params = useSearchParams();
  const router = useRouter();
  const clientID = params.get("client_id") || "";
  const redirectURI = params.get("redirect_uri") || "";
  const scope = params.get("scope") || "";
  const state = params.get("state") || "";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(
    clientID && redirectURI && scope
      ? ""
      : "This authorization link is incomplete.",
  );
  const info = useData<{ name: string; description: string }>(
    clientID && redirectURI && scope
      ? `/oauth/authorize?client_id=${encodeURIComponent(clientID)}&redirect_uri=${encodeURIComponent(redirectURI)}&scope=${encodeURIComponent(scope)}`
      : null,
  );
  async function decide(approve: boolean) {
    setBusy(true);
    setError("");
    try {
      const result = await post<{ redirect_to: string }>("/oauth/authorize", {
        client_id: clientID,
        redirect_uri: redirectURI,
        scope,
        state,
        approve,
      });
      window.location.href = result.redirect_to;
    } catch (decideError) {
      setError((decideError as Error).message);
      setBusy(false);
    }
  }
  return (
    <div className="form-page">
      <div className="page-heading">
        <div>
          <h1>Authorize application</h1>
          <p>Review what this application is asking to do before continuing.</p>
        </div>
      </div>
      <ErrorMessage error={error || info.error} />
      {info.loading ? (
        <Loading />
      ) : info.data ? (
        <section className="panel form-panel">
          <h2>{info.data.name}</h2>
          {info.data.description && (
            <p className="muted">{info.data.description}</p>
          )}
          <p>
            This application is requesting <strong>{scope}</strong> access to
            your account.
          </p>
          <div className="form-actions">
            <button
              className="button primary"
              disabled={busy}
              onClick={() => decide(true)}
            >
              Approve
            </button>
            <button
              className="button"
              disabled={busy}
              onClick={() => {
                decide(false);
                router.refresh();
              }}
            >
              Deny
            </button>
          </div>
        </section>
      ) : null}
    </div>
  );
}
