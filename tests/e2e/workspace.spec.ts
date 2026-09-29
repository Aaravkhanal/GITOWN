import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("account security, project collaboration, Git transport, and responsive navigation", async ({
  page,
  browser,
}) => {
  test.setTimeout(240_000);
  const username = `builder-${Date.now().toString(36)}`;
  const collaboratorUsername = `collab-${Date.now().toString(36)}`;
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
  await page.getByRole("link", { name: "Signed-in devices" }).click();
  await expect(
    page.getByRole("heading", { name: "Your active sessions." }),
  ).toBeVisible();
  await expect(page.getByText("Current device", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Account security" }).click();
  await page.getByLabel("Current password").fill("test-only-strong-password");
  await page
    .getByLabel("New password", { exact: true })
    .fill("changed-test-password");
  await page.getByLabel("Confirm new password").fill("changed-test-password");
  await page.getByLabel("Revoke every personal access token").uncheck();
  await page
    .getByRole("button", { name: "Change password", exact: true })
    .click();
  await expect(
    page.getByText("Password changed. Other browser sessions were signed out."),
  ).toBeVisible();
  await page.goto("/");
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
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page
    .getByLabel("File contents")
    .fill(
      "# first-project\n\nWelcome to your new repository on GITOWN.\n\nEdited safely in the browser.\n",
    );
  await page.getByLabel("Commit message").fill("Improve the README in GITOWN");
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response
          .url()
          .includes(`/api/v1/repos/${username}/first-project/contents`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    ),
    page.getByRole("button", { name: "Save commit", exact: true }).click(),
  ]);
  await expect(
    page.getByText("Edited safely in the browser.", { exact: true }),
  ).toBeVisible();
  const rawLink = page.getByRole("link", { name: "Raw", exact: true });
  await expect(rawLink).toHaveAttribute(
    "href",
    /\/raw\?ref=main&path=README\.md/,
  );
  await page.getByRole("button", { name: "History", exact: true }).click();
  await expect(
    page.locator(".commit-message", {
      hasText: "Improve the README in GITOWN",
    }),
  ).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "README.md File" }),
  ).toHaveCount(0);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page
    .getByLabel("Description")
    .fill("Updated from repository settings.");
  await page.getByLabel("public").check();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().includes(`/api/v1/repos/${username}/first-project`) &&
        response.request().method() === "PATCH" &&
        response.ok(),
    ),
    page.getByRole("button", { name: "Save settings" }).click(),
  ]);
  await expect(
    page.getByText("Repository settings saved.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Repository topics" })
    .fill("go, collaboration");
  await page.getByRole("button", { name: "Save topics" }).click();
  await expect(
    page
      .getByRole("region", { name: "Repository topics" })
      .getByText("collaboration"),
  ).toBeVisible();
  await page.goto("/explore?topic=collaboration");
  await expect(
    page.getByRole("link", { name: /first-project/ }).first(),
  ).toBeVisible();
  await page.getByLabel("Sort repositories").selectOption("trending");
  await expect(
    page.getByRole("link", { name: /first-project/ }).first(),
  ).toBeVisible();
  await page.goto(`/repos/${username}/first-project`);
  await page.getByRole("button", { name: "Spark · 0" }).click();
  await expect(page.getByRole("button", { name: "Sparked · 1" })).toBeVisible();
  await page.goto("/settings/profile");
  await page.getByLabel("Bio").fill("Building and sharing on GITOWN.");
  await page.getByRole("button", { name: "Save profile" }).click();
  await expect(page.getByRole("status")).toHaveText("Profile saved.");
  await page.getByRole("checkbox", { name: "first-project" }).check();
  await page.getByRole("button", { name: "Save showcase" }).click();
  await expect(page.getByRole("status")).toHaveText("Showcase saved.");
  await page.getByRole("link", { name: "View profile" }).click();
  await expect(page.getByText("Building and sharing on GITOWN.")).toBeVisible();
  await expect(page.getByText("0 followers · 0 following")).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "Showcase repositories" })
      .getByText("first-project"),
  ).toBeVisible();
  await page.goto(`/repos/${username}/first-project/settings`);
  await expect(
    page.getByRole("heading", { name: "Merge guard" }),
  ).toBeVisible();
  await page.getByLabel("Required approvals").fill("0");
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().includes("/branch-rules?branch=main") &&
        response.request().method() === "PUT" &&
        response.ok(),
    ),
    page.getByRole("button", { name: "Save merge guard" }).click(),
  ]);
  await expect(
    page.getByText("Branch rule saved.", { exact: true }),
  ).toBeVisible();
  const publicVisitor = await browser.newContext();
  const publicVisitorPage = await publicVisitor.newPage();
  await publicVisitorPage.goto(`/repos/${username}/first-project`);
  await expect(
    publicVisitorPage.getByRole("heading", {
      name: `${username} / first-project`,
    }),
  ).toBeVisible();
  await expect(
    publicVisitorPage.getByRole("link", { name: "Settings", exact: true }),
  ).toHaveCount(0);
  await publicVisitor.close();
  await page.getByLabel("private").check();
  await Promise.all([
    page.waitForResponse(
      (response) =>
        response.url().includes(`/api/v1/repos/${username}/first-project`) &&
        response.request().method() === "PATCH" &&
        response.ok(),
    ),
    page.getByRole("button", { name: "Save settings" }).click(),
  ]);
  await expect(
    page.getByText("Repository settings saved.", { exact: true }),
  ).toBeVisible();
  const collaborator = await browser.newContext();
  const appOrigin = new URL(page.url()).origin;
  const registration = await collaborator.request.post(
    `${appOrigin}/api/v1/auth/register`,
    {
      headers: { Origin: appOrigin },
      data: {
        username: collaboratorUsername,
        email: `${collaboratorUsername}@example.test`,
        password: "collaborator-test-password",
        display_name: "Collaborator",
      },
    },
  );
  expect(registration.ok()).toBeTruthy();
  await page.getByLabel("Username", { exact: true }).fill(collaboratorUsername);
  await page
    .getByRole("button", { name: "Add collaborator", exact: true })
    .click();
  await expect(page.getByText(`@${collaboratorUsername}`)).toBeVisible();
  const collaboratorPage = await collaborator.newPage();
  await collaboratorPage.goto(`/repos/${username}/first-project`);
  await expect(
    collaboratorPage.getByRole("heading", {
      name: `${username} / first-project`,
    }),
  ).toBeVisible();
  await expect(
    collaboratorPage.getByRole("link", { name: "Settings", exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel(`Role for ${collaboratorUsername}`)
    .selectOption("triage");
  await expect(
    page.getByText(`Updated @${collaboratorUsername}.`, { exact: true }),
  ).toBeVisible();
  await collaboratorPage.goto(`/repos/${username}/first-project/issues`);
  await expect(
    collaboratorPage.getByRole("button", { name: "New issue", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: `Remove ${collaboratorUsername}` })
    .click();
  await expect(
    page.getByText(`Removed @${collaboratorUsername}.`, { exact: true }),
  ).toBeVisible();
  await collaboratorPage.reload();
  await expect(
    collaboratorPage
      .getByRole("alert")
      .filter({ hasText: "Repository not found" }),
  ).toBeVisible();
  await collaborator.close();
  await page.getByRole("link", { name: "Code", exact: true }).click();
  await page.getByRole("link", { name: "Issues", exact: true }).click();
  await page.getByRole("button", { name: "New label", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill("idea");
  await page.getByLabel("Color", { exact: true }).fill("6f42c1");
  await page
    .getByLabel("Description", { exact: true })
    .fill("A future improvement");
  await page.getByRole("button", { name: "Create label", exact: true }).click();
  await expect(page.locator(".label-chip", { hasText: "idea" })).toBeVisible();
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
  await page.getByLabel("Add label").selectOption({ label: "idea" });
  await expect(
    page.locator(".assigned-label .label-chip", { hasText: "idea" }),
  ).toBeVisible();
  await page.getByLabel("Assign person").selectOption(username);
  await expect(
    page
      .getByRole("region", { name: "Issue assignees" })
      .getByText(`@${username}`),
  ).toBeVisible();
  await page.getByRole("button", { name: "New milestone" }).click();
  await page.getByLabel("Milestone title").fill("First release");
  await page.getByLabel("Due date optional").fill("2026-12-31");
  await page.getByRole("button", { name: "Create milestone" }).click();
  await expect(
    page.getByRole("region", { name: "Milestones" }).getByText("First release"),
  ).toBeVisible();
  await page
    .getByLabel("Assign milestone")
    .selectOption({ label: "First release" });
  await expect(
    page.getByRole("region", { name: "Milestones" }).getByText(/1 open/),
  ).toBeVisible();
  await page.getByRole("link", { name: "Board", exact: true }).click();
  await page.getByLabel("Status for issue #1").selectOption("progress");
  await expect(
    page
      .getByRole("region", { name: "In progress column" })
      .getByText("Design the next feature"),
  ).toBeVisible();
  await page.getByRole("link", { name: "Issues", exact: true }).click();
  await page
    .getByRole("button", { name: "Design the next feature", exact: true })
    .click();
  await page.getByLabel("Add a comment").fill("I can discuss this issue.");
  await page.getByRole("button", { name: "Comment", exact: true }).click();
  await expect(page.getByText("I can discuss this issue.")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Unfollow issue" }),
  ).toBeVisible();
  const commenterUsername = `commenter-${Date.now().toString(36)}`;
  const commenter = await browser.newContext();
  const commenterRegistration = await commenter.request.post(
    `${appOrigin}/api/v1/auth/register`,
    {
      headers: { Origin: appOrigin },
      data: {
        username: commenterUsername,
        email: `${commenterUsername}@example.test`,
        password: "commenter-test-password",
        display_name: "Commenter",
      },
    },
  );
  expect(commenterRegistration.ok()).toBeTruthy();
  const membership = await page.request.post(
    `${appOrigin}/api/v1/repos/${username}/first-project/members`,
    {
      headers: { Origin: appOrigin },
      data: { username: commenterUsername, role: "read" },
    },
  );
  expect(membership.ok()).toBeTruthy();
  const reply = await commenter.request.post(
    `${appOrigin}/api/v1/repos/${username}/first-project/issues/1/comments`,
    {
      headers: { Origin: appOrigin },
      data: { body: "A teammate replied to this issue." },
    },
  );
  expect(reply.ok()).toBeTruthy();
  await commenter.close();
  await page.goto("/inbox");
  await expect(
    page.getByRole("link", { name: /commented on Design the next feature/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Mark read" }).click();
  await expect(page.getByText("0 unread")).toBeVisible();
  await page.goto(`/repos/${username}/first-project/issues`);
  await page
    .getByRole("button", { name: "Design the next feature", exact: true })
    .click();
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
  await page.getByRole("link", { name: "Unite requests", exact: true }).click();
  await page
    .getByRole("button", { name: "New unite request", exact: true })
    .click();
  await page.getByLabel("Title", { exact: true }).fill("Add a welcome message");
  await page
    .getByRole("button", { name: "Create unite request", exact: true })
    .click();
  await expect(
    page.getByText("These branches can be merged", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("No formal reviews yet.")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Submit review", exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel("Add to the discussion")
    .fill("Ready for a careful review.");
  await page
    .getByRole("region", { name: "Unite request discussion" })
    .getByRole("button", { name: "Comment", exact: true })
    .click();
  await expect(page.getByText("Ready for a careful review.")).toBeVisible();
  await page.getByRole("button", { name: "Close unite request" }).click();
  await expect(
    page.getByRole("button", { name: "Reopen unite request" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Reopen unite request" }).click();
  await expect(
    page.getByRole("button", { name: "Close unite request" }),
  ).toBeVisible();
  await expect(page.locator(".diff-panel")).toContainText(
    "A real Git branch, merged from the browser.",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await page
    .getByRole("button", { name: "Merge unite request", exact: true })
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

  await page.goto(`/repos/${username}/first-project/settings`);
  await page.getByLabel("Repository name").fill("renamed-project");
  await page
    .getByRole("button", { name: "Rename repository", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: `${username} / renamed-project` }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Archive", exact: true }).click();
  await expect(
    page.getByText("This repository is archived and read-only.", {
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Unarchive", exact: true }).click();
  await expect(
    page.getByText("This repository is archived and read-only.", {
      exact: true,
    }),
  ).toHaveCount(0);
  await page.getByRole("link", { name: "Settings", exact: true }).click();
  page.once("dialog", (dialog) => dialog.accept("renamed-project"));
  await page
    .getByRole("button", { name: "Delete repository", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Welcome back, Aarav." }),
  ).toBeVisible();
  await page
    .getByRole("link", { name: "Deleted repositories", exact: true })
    .click();
  const restore = page.getByRole("button", { name: "Restore", exact: true });
  await expect(restore).toBeVisible();
  await restore.click();
  await expect(
    page.getByRole("heading", { name: `${username} / renamed-project` }),
  ).toBeVisible();

  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "renamed-project", exact: true }),
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
  const mobileLayout = await page.evaluate(() => ({
    fits: document.documentElement.scrollWidth <= window.innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
    overflowing: Array.from(document.querySelectorAll<HTMLElement>("*"))
      .map((element) => {
        const bounds = element.getBoundingClientRect();
        return {
          tag: element.tagName,
          className: element.className?.toString().slice(0, 80),
          text: element.textContent?.trim().slice(0, 80),
          left: bounds.left,
          right: bounds.right,
          width: bounds.width,
        };
      })
      .filter(
        (element) =>
          element.left < -0.5 || element.right > window.innerWidth + 0.5,
      )
      .slice(0, 20),
  }));
  expect(mobileLayout.fits, JSON.stringify(mobileLayout, null, 2)).toBeTruthy();
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
