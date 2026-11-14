# Passkeys and password managers in the Browser

## What happens today

The Browser's pages are WKWebViews. In them `PublicKeyCredential` exists, but
`PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()` and
`isConditionalMediationAvailable()` both answer `false` (measured in the dev copy on
macOS 15.7.9, on a public WebAuthn demo site). A site that asks first (Google does) then
never offers a passkey. A direct `navigator.credentials.get()` from a page without focus is
refused at once (`NotAllowedError: The document is not focused.`).

The reason: WebKit asks AuthenticationServices whether this app may make passkey requests
for the site, and for an app that is not an approved browser the answer is no, except for
domains the app itself owns (associated domains). There is no WebKit setting that changes
this.

## What passkeys need

An entitlement that Apple grants on request:
[`com.apple.developer.web-browser.public-key-credential`](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.web-browser.public-key-credential)
(macOS 13.3+). It covers passkeys and security keys for any site, including passkeys kept
in a third-party manager such as 1Password, which then appear in the system sheet.

- Only the **Account Holder of an organization** developer account can request it, with
  the form at <https://developer.apple.com/contact/request/macos-browsers-passkeys/>.
  Apple reviews the app as a browser. It must:
  - declare http and https links;
  - open with an address field, search, or bookmarks;
  - go to the address it is given and show it unchanged.
- When granted, it arrives as a managed capability on the App ID. A **Developer ID
  provisioning profile** made after that carries it. Other Developer ID browsers do this,
  but approval has been reported as slow.
- The app is then signed with that entitlement, the profile embedded, and the hardened
  runtime on. The build script does this when it is given the profile (below).

### Steps only the owner can do

1. Check the developer account type at <https://developer.apple.com/account> (Membership
   details). The Developer ID certificate this Mac signs with is in a personal name, which
   suggests an **individual** account. Apple's page says the request needs an
   organization account. If it is individual, passkeys for other sites are not available
   to this app until the app is under an organization account (which needs a legal entity
   and a D-U-N-S number).
2. If it is an organization account and you are its Account Holder, send the request form
   for the bundle id `com.hollislabs.basalisk`. Ask for the dev id
   `com.hollislabs.basalisk.dev` too, so the dev copy can be tried first.
3. After approval, in Certificates, Identifiers & Profiles:
   - register (or open) the explicit App ID `com.hollislabs.basalisk`;
   - turn on the browser public-key-credential capability;
   - create a **Developer ID** provisioning profile for it with the Developer ID
     Application certificate this Mac signs with;
   - download it. `security cms -D -i <file>` shows the entitlement under `Entitlements`.
4. Build the dev copy with the profile and try it (next section) before the real app is
   rebuilt.

### Building with the profile

```sh
BASALISK_PROVISION_PROFILE=~/Downloads/Basalisk_Dev.provisionprofile \
BASALISK_BUNDLE_ID=com.hollislabs.basalisk.dev BASALISK_APP_NAME="Basalisk Dev" \
  scripts/build-macos-app.sh
```

The script checks that the profile grants the entitlement and is for this bundle id
(`TEAM.bundle-id`), and stops if not. It then:

- adds http and https to `CFBundleURLTypes`;
- copies the profile to `Contents/embedded.provisionprofile`;
- signs with the hardened runtime and with the entitlements the profile names
  (application identifier, team identifier, the passkey entitlement).

Without `BASALISK_PROVISION_PROFILE` the build is exactly as before.

A differently signed app can lose its Accessibility and Input Monitoring grants. Try the
dev copy first; install the real one knowingly, and re-grant in System Settings → Privacy &
Security if asked.

**To try it:** in the dev copy's Browser, open <https://webauthn.io>, register a test
passkey, then sign in with it. The system passkey sheet should appear and offer iCloud
Keychain and 1Password. Then try the Google sign-in.

## 1Password

| Route | Gives | Needs | Status |
|---|---|---|---|
| **Universal Autofill** (⌘\\ in any app) | usernames, passwords; codes not documented | 1Password 8, Accessibility permission for 1Password; nothing in Basalisk | Read in 1Password's docs; WKWebView exposes the same accessibility tree as Safari, so it should work. Not tried in Basalisk. |
| **System AutoFill provider** | passkeys (and passwords) from 1Password in the system sheet | the passkey entitlement above | 1Password's AutoFill extension is installed and enabled on this Mac (checked with `pluginkit`). Works for passkeys only once the entitlement is granted. |
| **1Password browser extension** in WKWebExtension (macOS 15.4+) | passwords, codes, 1Password passkeys (the extension handles WebAuthn in the page) | Basalisk loading web extensions and relaying native messaging. 1Password checks the calling browser's code signature against its own list, so the desktop-app connection probably fails; standalone sign-in might not. | Read only; unproven. A sizeable build for an uncertain result. |
| **`op` CLI fill** | passwords and one-time codes, no passkeys | `op` with "Integrate with 1Password CLI" (Touch ID), and a fill command in Basalisk that only the owner can start | `op item get --fields`, `--otp` documented. Would need our own fill path. |
