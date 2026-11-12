# AnySave for Safari (macOS)

AnySave's extension code in [`extension/`](../extension) is a standard Manifest V3 Web Extension and works in Safari 16.4+ alongside the native `AnySave.app` macOS application.

## Two ways to use AnySave from Safari

### 1. Instant URL Scheme / Bookmarklet (No Xcode needed)
Once `AnySave.app` is installed in `/Applications`, it registers the `anysave://` URL handler on macOS.
In Safari, you can:
- Copy any video or file link (`⌘+C`) and press **`⌘+Shift+Y`** (or turn on **Clipboard Watch** in `AnySave.app`), or
- Drag any link from Safari onto the **AnySave Floating Drop Box**, or
- Add a Safari bookmark with the address:
  ```javascript
  javascript:location.href='anysave://?url='+encodeURIComponent(location.href)
  ```
  Clicking that bookmark sends the current Safari page directly to `AnySave.app`.

### 2. Full Safari Web Extension (Xcode converter)
If you have full Xcode installed on macOS, run:
```bash
xcrun safari-web-extension-converter ../extension \
  --project-location ./build \
  --app-name "AnySave Safari" \
  --bundle-identifier com.vidgrab.safari \
  --swift \
  --macos-only
```
Then enable **Develop → Allow Unsigned Extensions** in Safari Settings and check **AnySave** under **Safari → Settings → Extensions**.

