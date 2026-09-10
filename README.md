# chap-arena-v2

Configure a practice field's access point and Cisco switch from one page. Enter
team numbers directly into the six station slots, set a common Wi-Fi password,
and press **Apply**. Each team's Driver Station controls robot operation.

This is a networking-only fork of [Cheesy Arena](https://github.com/Team254/cheesy-arena).
It does not start FMS listeners, match control, scoring, TBA publishing, displays,
team signs, or SCC switch management.

## Run from source

1. Install the Go version specified in [go.mod](go.mod).
2. Clone this repository and open a terminal in its directory.
3. Build and run the application:

   ```sh
   go build -o chap-arena-v2
   ./chap-arena-v2
   ```

   On Windows, build with `go build -o chap-arena-v2.exe` and run `chap-arena-v2.exe`.

4. Open the server computer's address on port `8080` in your browser.

The binary includes its web assets. It creates `chap-arena.db` in the working
directory. Use `-db /path/to/chap-arena.db` to choose a different database, or
`-listen :8081` to choose a different HTTP port. Stop the application before
copying its database to another computer.

## Configure the network

Use the Vivid-Hosting field AP and main Cisco switch from Cheesy Arena's
[advanced networking setup](https://github.com/Team254/cheesy-arena/wiki/Configuring-New-Networking-Equipment#advanced-networking).
The existing [switch configuration](switch_config.txt) remains the starting point
for the Cisco switch. No additional red or blue SCC switches are required.

1. Open **Settings** and enter the AP address, API password, channel, switch
   address, and switch password. Keep **Enable advanced network security** checked.
2. Click **Save**, then return to **Stations**. Saving settings does not contact hardware.
3. Set the common password. Passwords must contain 8 to 63 printable ASCII characters.
4. Enter team numbers in the station slots. A blank slot clears that station's
   team Wi-Fi configuration and switch IP/DHCP configuration when you apply.
5. To use a different password for a team, click **Common** beside its number
   and enter a password override. Click **Use common password** to remove an override.
6. Click **Apply** and check both device results. If either device fails, correct
   the connection or settings, then click **Apply** to retry.

The AP uses each team number as its SSID. Program each robot radio separately
with that team number and the corresponding password. Apply configures the field
AP and switch; it does not program robot radios.

Team numbers can be entered without creating a team roster. Each override follows
its team number across stations and remains saved when the team is removed.
Changing the common password affects teams without overrides. Duplicate team
assignments are rejected. The existing switch addressing scheme supports team
numbers from 1 to 25599.

## Apply results

**AP Accepted** means the AP accepted the request and is applying it asynchronously.
The application does not verify that the AP finished applying the request.
**Switch Applied** means the Cisco configuration commands completed without a
reported command error. Neither result verifies the robot's connection.

If one device fails, the other device may already have changed. Apply does not
roll back partial changes. It saves assignments and passwords before contacting
hardware, so you can retry after a connection failure or application restart.

Only an explicit **Apply** changes hardware. Opening a page, saving settings,
and restarting the application do not configure either device. After a restart,
device results return to **Not applied** until the next Apply.

## Development

```sh
go fmt ./...
go test ./...
go test -race ./practice ./network -run 'TestApply|TestAPFailure|TestDisabled|TestSwitchRejects|TestSwitchConnection'
```

`practice/` owns the two-page application and Apply flow. It reuses the AP and
Cisco protocols in `network/` and BoltDB persistence in `model/`. Legacy event
packages remain in the source tree but are not started or exposed by this binary.

## License and attribution

Cheesy Arena was created by Team 254 and its
[contributors](https://github.com/Team254/cheesy-arena/graphs/contributors).
The original [LICENSE](LICENSE) applies, including its restrictions on
redistributing modifications.
