"use client";

import { useState } from "react";
import { api, post } from "@/lib/api";
import { CopyButton, ErrorMessage, Loading } from "./ui";
import { useData } from "./ui";

type MFAState = {
  enabled: boolean;
  recovery_codes_remaining: number;
  setup_pending: boolean;
};

export function MFASettings() {
  const [version, setVersion] = useState(0);
  const state = useData<MFAState>("/user/mfa", version);
  const [secret, setSecret] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (state.loading) return <Loading />;
  return (
    <section className="panel form-panel">
      <h2>Two-step sign-in</h2>
      <ErrorMessage error={state.error || error} />
      {codes.length > 0 ? (
        <div className="info-box">
          <strong>Save these recovery codes now.</strong>
          <p>Each code works once. GITOWN will not show them again.</p>
          <div className="code-list">
            {codes.map((code) => (
              <code key={code}>{code}</code>
            ))}
          </div>
          <CopyButton text={codes.join("\n")} label="Copy recovery codes" />
          <button className="button small-button" onClick={() => setCodes([])}>
            Done
          </button>
        </div>
      ) : state.data?.enabled ? (
        <>
          <p className="muted">
            Authenticator codes are required at sign-in.{" "}
            {state.data.recovery_codes_remaining} recovery codes remain.
          </p>
          <form
            className="inline-form"
            onSubmit={async (event) => {
              event.preventDefault();
              setBusy(true);
              setError("");
              const data = new FormData(event.currentTarget);
              const code = String(data.get("code") || "").trim();
              try {
                await api("/user/mfa/disable", {
                  method: "POST",
                  body: JSON.stringify({
                    current_password: data.get("password"),
                    ...(code.startsWith("GITOWN-")
                      ? { recovery_code: code }
                      : { code }),
                  }),
                });
                setVersion((value) => value + 1);
              } catch (submitError) {
                setError((submitError as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <label>
              Current password
              <input
                type="password"
                name="password"
                autoComplete="current-password"
                required
              />
            </label>
            <label>
              Authenticator or unused recovery code
              <input name="code" autoComplete="one-time-code" required />
            </label>
            <button className="button danger" disabled={busy}>
              {busy ? "Disabling…" : "Disable MFA"}
            </button>
          </form>
        </>
      ) : secret ? (
        <>
          <p>
            Add this key to an authenticator app, then enter its current code.
          </p>
          <div className="token-value">
            <code>{secret}</code>
            <CopyButton text={secret} label="Copy setup key" />
          </div>
          <form
            className="inline-form"
            onSubmit={async (event) => {
              event.preventDefault();
              setBusy(true);
              setError("");
              const data = new FormData(event.currentTarget);
              try {
                const result = await post<{ recovery_codes: string[] }>(
                  "/user/mfa/confirm",
                  {
                    current_password: data.get("password"),
                    code: data.get("code"),
                  },
                );
                setSecret("");
                setCodes(result.recovery_codes || []);
                setVersion((value) => value + 1);
              } catch (submitError) {
                setError((submitError as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <label>
              Current password
              <input
                type="password"
                name="password"
                autoComplete="current-password"
                required
              />
            </label>
            <label>
              Authenticator code
              <input
                name="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                required
              />
            </label>
            <button className="button primary" disabled={busy}>
              {busy ? "Checking…" : "Enable two-step sign-in"}
            </button>
          </form>
          <button className="text-button" onClick={() => setSecret("")}>
            Cancel setup
          </button>
        </>
      ) : (
        <>
          <p className="muted">
            Require an authenticator code in addition to your password.
          </p>
          <form
            className="inline-form"
            onSubmit={async (event) => {
              event.preventDefault();
              setBusy(true);
              setError("");
              const data = new FormData(event.currentTarget);
              try {
                const result = await post<{ secret: string }>(
                  "/user/mfa/setup",
                  {
                    current_password: data.get("password"),
                  },
                );
                setSecret(result.secret);
              } catch (submitError) {
                setError((submitError as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            <label>
              Confirm your password
              <input
                type="password"
                name="password"
                autoComplete="current-password"
                required
              />
            </label>
            <button className="button" disabled={busy}>
              {busy ? "Starting…" : "Set up MFA"}
            </button>
          </form>
        </>
      )}
    </section>
  );
}
