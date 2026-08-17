# Introduction

### Purpose
RGOClient is a native desktop client for Stoat.
No Electron, no webview, and only one binary. 
It was developed to test and improve the capabilites of the underlying library this frontend runs on — [revoltgo](https://github.com/sentinelb51/revoltgo).

### Screenshots
<details>
<summary>Click to see images</summary>
  
#### Authentication window
> <img width="302" height="358" alt="image" src="https://github.com/user-attachments/assets/eac56802-6b4e-4f4f-aa38-828fd2a7b3fc" />

#### Messaging area
> <img width="1202" height="632" alt="image" src="https://github.com/user-attachments/assets/57161997-9682-452c-b856-fea679fa7719" />

> <img width="1202" height="632" alt="image" src="https://github.com/user-attachments/assets/57675b99-9677-463d-8ae5-2b5c03948221" />

#### User profiles
> <img width="1202" height="632" alt="image" src="https://github.com/user-attachments/assets/9c395baf-d4c0-4ac2-ba89-6a64a0b05043" />

#### Settings page
> <img width="1202" height="632" alt="image" src="https://github.com/user-attachments/assets/4ce15d15-4e98-489b-bcf9-ab696da7ba43" />
</details>

### Platforms
Currently, this project has only been tested on Windows. 
There are plans to expand support to other operating systems once the project is mature.  

### Features
Nothing impressive yet, but we are working on that.

### Resource usage

### Architecture
Only `internal/client` sees `revoltgo`;
Only `internal/ui` sees Fyne

