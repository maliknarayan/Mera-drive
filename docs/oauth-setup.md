# Google OAuth setup

SangamDrive needs one OAuth client. The same client is reused for every Google
account a user connects — you do not need one per account.

## 1. Create a project

<https://console.cloud.google.com/projectcreate>

Name it anything; `sangamdrive` is fine.

## 2. Enable the APIs

**APIs & Services → Library**, then enable:

- **Google Drive API** — file operations
- **Google People API** — profile name and avatar for account cards

## 3. Configure the consent screen

**APIs & Services → OAuth consent screen**

| Field           | Value                                              |
| --------------- | -------------------------------------------------- |
| User type       | **External** (unless you have a Workspace tenant)  |
| App name        | SangamDrive                                        |
| Support email   | Your email                                         |
| Authorised domain | Your domain, e.g. `example.com`                  |

Add these scopes:

```
openid
https://www.googleapis.com/auth/userinfo.email
https://www.googleapis.com/auth/userinfo.profile
https://www.googleapis.com/auth/drive.file
https://www.googleapis.com/auth/drive
```

`drive` is a **restricted** scope. Read [Testing vs published](#testing-vs-published)
before you rely on it.

## 4. Create the client

**APIs & Services → Credentials → Create credentials → OAuth client ID**

- Application type: **Web application**
- Authorised JavaScript origins: your web origin, e.g. `https://drive.example.com`
- Authorised redirect URI — this must match **exactly**:

```
{API_BASE_URL}/api/v1/auth/google/callback
```

| Setup                        | Redirect URI                                                 |
| ---------------------------- | ------------------------------------------------------------ |
| Local development            | `http://localhost:8080/api/v1/auth/google/callback`          |
| Same-origin behind a proxy   | `https://drive.example.com/api/v1/auth/google/callback`      |

A trailing slash, `http` instead of `https`, or a different port all cause
`redirect_uri_mismatch`.

## 5. Put the credentials in .env

```bash
GOOGLE_CLIENT_ID=1234567890-abcdef.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-...
API_BASE_URL=http://localhost:8080
```

SangamDrive derives the redirect URL from `API_BASE_URL`, so there is no separate
variable to keep in sync.

## Testing vs published

While the consent screen is in **Testing**:

- Only accounts on the **Test users** list can connect. Add every Google account
  you intend to link.
- Refresh tokens expire after **7 days**. Accounts will show
  "Reconnect required" weekly. This is a Google policy, not a SangamDrive bug.

Moving to **In production** removes both limits. If you request the `drive` scope,
Google requires app verification — for a personal self-hosted instance, staying in
Testing and re-connecting weekly, or using only `drive.file`, avoids that process
entirely.

## Choosing a scope

| Scope        | Sees                                                  | Verification |
| ------------ | ------------------------------------------------------ | ------------ |
| `drive.file` | Only files created or explicitly opened via SangamDrive | Not required |
| `drive`      | Everything in the Drive                                 | Required to publish |

`drive.file` is the honest default and the one the UI recommends. Users can
upgrade an individual account later without disconnecting it.

## Troubleshooting

**`redirect_uri_mismatch`** — the URI in the Google Console differs from
`{API_BASE_URL}/api/v1/auth/google/callback`. Compare them character by character.

**`access_denied`** — the account is not on the Test users list, or the user
declined consent.

**No refresh token returned** — Google only issues one on first consent. SangamDrive
sends `access_type=offline&prompt=consent` to force a fresh one; if you see this,
revoke SangamDrive at <https://myaccount.google.com/permissions> and reconnect.

**Accounts break every 7 days** — expected in Testing mode. See above.
