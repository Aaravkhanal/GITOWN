# Repository Wiki preview

The Wiki is the first Phase 12 slice. Pages are ordinary UTF-8 Markdown files at `.gitown/wiki/<slug>.md` on a repository's default branch. You can create or edit them from the Wiki tab or with Git, and the page's history is normal Git history. There is no separate wiki database or hidden copy to export.

Readers inherit the repository's visibility rules. Writers need repository write access. Browser saves use the branch head the editor read and reject a stale save; they also obey the same branch rules as browser code edits, including Require Unite, restricted push, and required signatures. If the default branch is protected against direct edits, change the wiki file on another branch and open a Unite request. An archived repository is read-only.

Pages use lowercase slugs of letters, numbers, and hyphens, up to 80 characters. A page is limited to 128 KiB in the browser editor. The display uses the same safe, deliberately small Markdown renderer as Drop notes: headings, emphasis, inline and fenced code, HTTPS/HTTP links, and lists. Raw HTML is displayed as text; it is never injected into the page.

This preview has no image uploads, attachments, redirects, wiki-specific search, page-level permissions, or custom domains. Those remain part of the Phase 12 gate. Repository backup/export already includes wiki pages because they are Git files.
