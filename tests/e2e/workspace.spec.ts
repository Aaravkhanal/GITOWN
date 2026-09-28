import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("account, repository, issue, Git token, privacy, and responsive navigation", async ({
  page,
  browser,
}) => {
  const username = `builder-${Date.now().toString(36)}`;
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/register");
  await page.getByLabel("Display name").fill("Aarav");
  await page.getByLabel("Username", { exact: true }).fill(username);
  await page.getByLabel("Email address").fill(`${username}@example.test`);
  await page
    .getByLabel("Password", { exact: true })
    .fill("test-only-strong-password");
  await page
    .getByRole("button", { name: "Create account", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Welcome back, Aarav." }),
  ).toBeVisible();
  await page
    .getByRole("link", { name: "New repository", exact: true })
    .last()
    .click();
  await page.getByLabel("Repository name").fill("first-project");
  await page
    .getByLabel("Description optional")
    .fill("A home for my next big idea. Built with GITOWN.");
  await page
    .getByRole("button", { name: "Create repository", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: `${username} / first-project` }),
  ).toBeVisible();
  await page.getByRole("button", { name: "README.md File" }).click();
  await expect(
    page.getByText("Welcome to your new repository on GITOWN.", {
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Issues", exact: true }).click();
  await page.getByRole("button", { name: "New issue", exact: true }).click();
  await page
    .getByLabel("Title", { exact: true })
    .fill("Design the next feature");
  await page
    .getByLabel("Description", { exact: true })
    .fill("Keep every change small, useful, and tested.");
  await page.getByRole("button", { name: "Create issue", exact: true }).click();
  await page
    .getByRole("button", { name: "Design the next feature", exact: true })
    .click();
  await expect(
    page.getByText("Keep every change small, useful, and tested."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close issue", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "No open issues" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Closed", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Design the next feature" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Access tokens", exact: true }).click();
  await page.getByLabel("Token name").fill("My terminal");
  await page
    .getByRole("button", { name: "Generate token", exact: true })
    .click();
  await expect(
    page.getByText("Your token is ready. Copy it now."),
  ).toBeVisible();
  await expect(page.locator(".secret-box code")).toContainText("gtn_");
  const token = (await page.locator(".secret-box code").textContent())!;
  await page.goto(`/repos/${username}/first-project`);
  await page
    .getByRole("button", { name: "Clone repository", exact: true })
    .click();
  const cloneCommand = (await page.locator(".clone-panel code").textContent())!;
  const cloneURL = cloneCommand.replace("git clone ", "");
  const work = join(mkdtempSync(join(tmpdir(), "gitown-browser-")), "project");
  const gitEnv = {
    ...process.env,
    GIT_TERMINAL_PROMPT: "0",
    GIT_CONFIG_COUNT: "1",
    GIT_CONFIG_KEY_0: "http.extraHeader",
    GIT_CONFIG_VALUE_0: `Authorization: Basic ${Buffer.from(`${username}:${token}`).toString("base64")}`,
  };
  const git = (args: string[], cwd?: string) =>
    execFileSync("git", ["-c", "credential.helper=", ...args], {
      cwd,
      env: gitEnv,
      stdio: "pipe",
    });
  git(["clone", cloneURL, work]);
  git(["config", "user.name", "Browser test"], work);
  git(["config", "user.email", "browser@example.test"], work);
  git(["switch", "-c", "feature/welcome"], work);
  writeFileSync(
    join(work, "welcome.txt"),
    "A real Git branch, merged from the browser.\n",
  );
  git(["add", "welcome.txt"], work);
  git(["commit", "-m", "Add a welcome message"], work);
  git(["push", "-u", "origin", "feature/welcome"], work);
  await page.reload();
  await page.getByRole("link", { name: "Pull requests", exact: true }).click();
  await page
    .getByRole("button", { name: "New pull request", exact: true })
    .click();
  await page.getByLabel("Title", { exact: true }).fill("Add a welcome message");
  await page
    .getByRole("button", { name: "Create pull request", exact: true })
    .click();
  await expect(
    page.getByText("These branches can be merged", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".diff-panel")).toContainText(
    "A real Git branch, merged from the browser.",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await page
    .getByRole("button", { name: "Merge pull request", exact: true })
    .click();
  await expect(
    page.getByText("Changes successfully merged", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "test-results/pull-request-merged.png",
    fullPage: true,
  });
  git(["switch", "main"], work);
  git(["pull", "--ff-only", "origin", "main"], work);
  await page.getByRole("link", { name: "Access tokens", exact: true }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(
    page.getByText("No access tokens yet.", { exact: false }),
  ).toBeVisible();

  const outsider = await browser.newContext();
  const outsiderPage = await outsider.newPage();
  await outsiderPage.goto(`/repos/${username}/first-project`);
  await expect(
    outsiderPage.getByRole("alert").filter({ hasText: "Repository not found" }),
  ).toBeVisible();
  await outsider.close();

  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "first-project", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "test-results/workspace-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: "test-results/workspace-mobile.png",
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  await page.getByRole("link", { name: "Access tokens", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Keys to your code." }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBeTruthy();
  expect(errors).toEqual([]);
});
