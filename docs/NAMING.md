# GITOWN naming system

GITOWN uses memorable product language without hiding standard Git concepts. The first mention in onboarding and documentation pairs the GITOWN term with the familiar term. Protocols, APIs, repository data, and Git commands retain their standard technical names.

| GITOWN term   | Standard concept   | Product meaning                                |
| ------------- | ------------------ | ---------------------------------------------- |
| Project       | Repository         | A Git repository and its collaboration space.  |
| Unite request | Pull request       | A proposal to combine one branch with another. |
| Remix         | Fork               | An independently owned copy of a project.      |
| District      | Organization       | A shared company or community space.           |
| Crew          | Team               | A permission-bearing group inside a district.  |
| Route         | Automated workflow | A build, test, or deployment workflow.         |
| Drop          | Release            | A published project version and its assets.    |
| Spark         | Star               | A signal that a user values a project.         |
| Town Hall     | Discussion         | A project or district community conversation.  |
| Showcase      | Pages/portfolio    | A hosted presentation of a project.            |
| Crate         | Package            | A published reusable software package.         |
| Board         | Project board      | Planning across tasks and unite requests.      |

## Language rules

1. Never rename Git primitives such as commit, branch, tag, remote, clone, push, pull, merge, or ref.
2. Pair a branded term with its standard term during onboarding, for example “Unite request (pull request).”
3. Keep stable API paths where changing them would break clients; branded aliases can be added later.
4. Prefer clear actions over decorative language. Error messages must explain the actual Git or policy failure.
5. New names ship with documentation, accessible labels, compatibility, and search synonyms.

The CLI action vocabulary is `bring`, `track`, `save`, `send`, `sync`, `unite`, `move`, and `look`. `gitown git` remains the compatibility escape hatch.
