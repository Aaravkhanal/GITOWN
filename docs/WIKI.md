# Repository Wiki preview

The Wiki is the first Phase 12 slice. Pages are ordinary UTF-8 Markdown files at `.gitown/wiki/<slug>.md` on a repository's default branch. You can create or edit them from the Wiki tab or with Git, and the page's history is normal Git history. There is no separate wiki database or hidden copy to export.

Readers inherit the repository's visibility rules. Writers need repository write access. Browser saves use the branch head the editor read and reject a stale save; they also obey the same branch rules as browser code edits, including Require Unite, restricted push, and required signatures. If the default branch is protected against direct edits, change the wiki file on another branch and open a Unite request. An archived repository is read-only.

Pages use lowercase slugs of letters, numbers, and hyphens, up to 80 characters. A page is limited to 128 KiB in the browser editor. The display uses the same safe, deliberately small Markdown renderer as Drop notes: headings, emphasis, inline and fenced code, HTTPS/HTTP links, and lists. Raw HTML is displayed as text; it is never injected into the page.

`GET /api/v1/repos/{owner}/{repo}/wiki-search?q=` runs a fixed-string `git grep` over `.gitown/wiki` on the default branch and returns the slug, line number, and matching text. The query is at most 80 characters and cannot start with `-`.

Attachments are Git files at `.gitown/wiki/attachments/<slug>/<filename>` and are included in clone, history, backup, and export. Browser uploads are limited to 512 KiB (the Git store's per-blob safety limit) and PNG, JPEG, GIF, WebP, or PDF with a matching extension. SVG, HTML, and arbitrary downloads are deliberately rejected; served assets use content sniffing, `nosniff`, and a restrictive content security policy. Writers upload against the branch head they have loaded, so a concurrent edit is rejected rather than overwritten. Readers inherit repository visibility. Existing attachment files in Git are only served if their bytes match one of the allowed types.

Attachments currently appear as a list below the page rather than being embedded by Markdown syntax. Redirects, page-level permissions, and custom domains are not supported. Repository backup/export includes wiki attachments because they are Git files.
