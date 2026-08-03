# Session Project Binding

## Problem

ADK sessions are scoped by `app_name + user_id + session_id`. Platform projects were added later, so a plain session list request can return sessions from several projects when the same user uses the same app in multiple projects.

That is why a session created while working on one project can appear in another project's session picker.

## Current Binding Contract

Project binding is stored in session state:

- `state.project_id`
- `state.projectId`

Both names are accepted for compatibility with backend tools and frontend naming styles.

## REST API Behavior

Create session:

```text
POST /apps/{app_name}/users/{user_id}/sessions?project_id={project_id}
POST /apps/{app_name}/users/{user_id}/sessions/{session_id}?project_id={project_id}
POST /apps/{app_name}/users/{user_id}/sessions?current_session_id={session_id}
```

If the request body does not already contain `state.project_id` or `state.projectId`, the controller writes the query `project_id` into both state keys. If only `current_session_id` is present, the controller reads that current session's project binding and copies it into the new session.

List app/user sessions:

```text
GET /apps/{app_name}/users/{user_id}/sessions?project_id={project_id}
GET /apps/{app_name}/users/{user_id}/sessions?current_session_id={session_id}
```

When `project_id` is present, only sessions whose state matches that project are returned. When only `current_session_id` is present, the backend resolves the current session's project first, then filters by that project.

List platform sessions:

```text
GET /platform/sessions?project_id={project_id}
```

When `project_id` is present, the admin list is filtered the same way.

## Frontend Requirement

Any project-aware session picker must pass either the current project id or the current session id to both create and list session calls. Without either parameter, the backend preserves ADK's original app/user session behavior and returns all sessions for that app/user pair.

## Future Direction

The durable version should add an indexed `project_id` column or a platform `project_sessions` table. State filtering is a compatibility bridge, not the final asset-state model.
