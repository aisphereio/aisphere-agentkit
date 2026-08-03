# Upload scripts

These scripts are operator/developer helpers for the platform Upload Center.
They are intentionally outside the agent prompt path: scripts talk to the Upload API and never paste file content into `newMessage.parts`.

## Upload a file

```powershell
.\scripts\uploads\upload-file.ps1 `
  -File E:\books\demo.txt `
  -Purpose book_source `
  -AppName book_dissector `
  -SessionId book001
```

## Preview a bounded slice

```powershell
.\scripts\uploads\preview-upload.ps1 -UploadId <upload_id> -MaxBytes 8192
```

## Attach an upload to the current artifact workspace

```powershell
.\scripts\uploads\attach-upload-artifact.ps1 `
  -UploadId <upload_id> `
  -AppName book_dissector `
  -SessionId book001 `
  -ArtifactName demo.txt
```

After attaching a book source, the `book_dissector` agent can call `book_split_from_artifact` with `source_artifact=demo.txt`.
