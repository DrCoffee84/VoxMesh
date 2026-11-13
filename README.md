[Español](README.es.md)

# VoxMesh

Voice and text chat for gaming or hanging out with friends, on **Windows**. It's a single program: no installer, no accounts and no servers in between. One person starts a room, shares the invite on WhatsApp and that's it. If whoever started the room leaves or loses their internet, the room keeps going with everyone else.

## Download

- 💻 **PC (Windows 10 or 11):** [**Download VoxMesh for Windows**](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe)
- 📱 **Phone as a microphone** (Android, optional): [**Download VoxMesh Mic**](https://github.com/DrCoffee84/VoxMesh/releases/download/v1.0.0/voxmesh-mic.apk)

The PC app updates itself: when a new version comes out, it asks whether you want to install it. **Everyone in the room needs the same version.**

## Install on the PC

1. Create a folder (for example `Documents\VoxMesh`) and move `voxmesh.exe` into it. The app saves its settings next to the `.exe`.
2. Double-click it. The first time you may see these warnings, which are normal (see [why](#is-it-safe) below):
   - **Your browser** says the file "isn't commonly downloaded" → **Keep**.
   - **"Windows protected your PC"** (blue screen) → **More info** → **Run anyway**.
   - **Windows Firewall** → **Allow access**. If you don't allow it, your friends won't be able to join your room.

## Install the phone app (optional)

It lets you use your phone as the PC's microphone. You can also do it without installing anything: on the PC, Settings (⚙️) → **📱 Phone microphone** → **Show QR**, and scan it with the phone's camera (both have to be on the same Wi-Fi).

1. Open the download link **on the phone**.
2. Android will ask for permission to install apps from the browser → **Settings** → turn on **Allow from this source** → go back.
3. If **Play Protect** warns that the app is unknown → **More details** → **Install anyway**.
4. Open *VoxMesh Mic*, tap **Escanear Código QR** (scan QR code; the phone app is in Spanish for now) and point it at the QR the PC shows.

If a new version won't install over the old one, uninstall the old one and try again.

## How to use it

**To start a room:**
1. Click **Start room** and pick a name.
2. At the top, next to the microphone, click **Invite** and pick the first line, **Internet**. The invite (`VM1-…`) is copied. If the button shows an orange clock, wait a few seconds: the app is opening the port on your router and getting your public IP (the activity log says when it's ready).
3. Paste it on WhatsApp for your friends.

**To join a room:**
1. Copy the invite you were sent.
2. Paste it in the **Join** box and click **Connect**.

**Good to know:**
- **Only share the invite with people you trust**: anyone who has it can join.
- **If Invite shows an orange or red line**, people can't join over the internet yet, and the activity log says what to do. *Orange*: it's still opening the port (wait), or your router is behind another one. *Red*: the port couldn't be opened ("Couldn't open the port"), your public IP couldn't be found, or your provider doesn't allow it. Click the red line to see the activity log. If you forwarded the port by hand on your router, use the orange **Internet** line below it.
- **Inviting over LAN, Radmin, Tailscale or ZeroTier**: in **Invite**, below Internet, are the addresses of your networks, marked "(same network or VPN)". They only work on the same network or VPN; over the internet use the **Internet** one. If you don't use them, you can hide them by turning off **Show LAN/VPN addresses when inviting** in Settings → Interface and system.
- Anyone in the room can invite with the same button: handy for someone who arrives late or dropped out and wants back in. The invite stops working when someone clicks "Start room" again.
- In Settings (⚙️) you pick your name, microphone and headphones, the noise filter and each friend's volume. Changes are heard right away, but they're only saved with **Save**.
- It also has chat with images and a soundboard with keyboard shortcuts.
- **Screen sharing**, made for helping a friend set something up: the **Share** button at the top shares your screen (you choose which one if you have several, and the resolution), and the others click **View screen** on your avatar to see it in its own window, fitted or at actual size to read small text. Only people who click it can see it, and while you're sharing a bar reminds you (you can change the resolution there) and a red dot shows on VoxMesh's taskbar button, even minimized.

## Is it safe?

Yes, and you don't have to take our word for it: you can check.

- **It's open source** ([MIT license](LICENSE)): all the code is in this repository and anyone can read it.
- **GitHub builds it, not a person**: both the `.exe` and the phone app are built by GitHub Actions from this same public code. Every build is recorded in the [Actions](https://github.com/DrCoffee84/VoxMesh/actions) tab.
- **Why do Windows and Android warn you?** Because the app isn't signed with a paid certificate (they cost hundreds of dollars a year) and it's not on the Play Store. Those warnings show up for any new unsigned program, not because something bad was detected.
- **What it does on your PC**: it doesn't install, doesn't ask for administrator rights and has no accounts or ads. It only connects to the others in your room, to GitHub (to check for updates), to `api.ipify.org` (to find your public IP when you start a room) and to your router (to open the port by itself).
- **Your voice and chat are encrypted** with a key that goes inside the invite: without it nobody can listen in or join. What the people in the room do see is your IP address, like in any direct call.
- **If you want to verify it yourself**: in PowerShell, `Get-FileHash .\voxmesh.exe` has to give the same number as [voxmesh.exe.sha256](https://github.com/DrCoffee84/VoxMesh/releases/latest/download/voxmesh.exe.sha256) (upper/lower case doesn't matter). You can also upload it to [VirusTotal](https://www.virustotal.com); one or two little-known antivirus engines flagging a new unsigned program is a common false positive.

## Common problems

- **"The host isn't answering"** (when joining): ask the host to check their activity log, which says whether the port could be opened and what to do. If the room is still open and the port is open, the invite is old (someone clicked "Start room" again): ask for a new one.
- **"This is a LAN/VPN invite"**: the host copied an address from their network or a VPN (Radmin, Tailscale, ZeroTier), which only works if you're on the same one. If not, ask for the **Internet** one.
- **Your friends hear themselves (echo)**: it happens if you use speakers or a TV. Turn on **"Using speakers: cancel echo"** in Settings → Audio input → Advanced settings → Noise and voice suppression. Not needed with headphones.
- **Nobody can join your room**: check your activity log, it says whether the port could be opened. If it says your router is **behind another one** (your provider's modem), forward the same port on the modem to your router too, or put the modem in bridge or DMZ mode. If the router didn't open it by itself (UPnP), turn on UPnP on your router or forward the UDP port the notice gives (47830 unless you changed it) to your PC's local IP, which is in the notice too. Some internet providers (especially mobile data and some fiber ones) don't allow direct connections: have someone else start the room, or have everyone install [Tailscale](https://tailscale.com) (free) and the host invite with **Invite** → the option that says **Tailscale**. If it says the room is ready and still nobody gets in, check the Windows firewall: the first time you start a room Windows asks whether to let VoxMesh through; tick private **and** public networks (or allow it in Windows Security → Firewall → "Allow an app").
- **After several hours the call dropped or nobody can join**: the port the app opens on your router lasts 8 hours (the activity log says until what time and warns when it runs out). Start the room again and share the new invite. If you play longer, raise the duration (up to 24 h) in Settings → Network.
- **Someone can't connect and the others can**: check that everyone has the same version.
- **Something else is wrong**: the document button (bottom right) or Settings → **Open logs folder** opens the logs folder. Send today's log to whoever gave you the app.

## More info

- [DESARROLLO.md](DESARROLLO.md) (in Spanish): how it works inside, how to build it and how to publish a version.
- [AGENT.md](AGENT.md): detailed technical map of the code.
- [LICENSE](LICENSE): MIT license.
