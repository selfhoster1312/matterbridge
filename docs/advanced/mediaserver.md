# Media server

matterbridge supports serving file attachments from an owned "media server",
instead of simply relaying file URLs from the bridged networks. This enables:

- to keep attachment URLs working when the remote network changes them, such
  as Discord does regularly
- to provide a public attachment URL, usable by text-only protocols (eg. IRC)
  when the bridge providing the attachment does not provide a public URL,
  such as Matrix with its authenticated media requiring custom HTTP headers
- to avoid leaking 3rd party client IPs and potentially hitting rate-limits
  with bridged networks or triggering other anti-interop mechanisms

**Running a mediaserver is only required if you bridge a text-only protocol
such as IRC.**

Two methods are available to feed matterbridge attachments to the media server:

- local folder
- S3 protocol

> [!WARNING]
> The `MediaServerUpload` and recommended `caddy` setup have been deprecated,
> because [caddyv2-upload](https://github.com/git001/caddyv2-upload) no longer
> supports the protocol we implemented back in the day.
> 
> Support for their newer protocol may be reimplemented in the future, and PRs
> are welcome for this.

## How attachments are handled

When you attach a file to your message on a certain network, what happens next
is very protocol-specific:

- some networks don't support file attachments at all, eg. IRC
- some networks send raw bytes over the wire, such as Mumble
- some networks make the image publicly reachable over HTTP, and send a URL
  (such as unencrypted XMPP OOB, or Discord)
- some networks require custom logic to retrieve the image, such
  as Matrix authenticated media

Not all networks support the same mechanisms, but the lowest common denominator
is the public URL which can always be transmitted as a text message. But when
the origin network providing attached files does not provide a public URL to
access those files, we need to be able to make our own URL out of the file.

That's what the media server is for. By sending the files to a remote server,
or placing them in a certain directory, if a web server is configured to serve
those files, then we can simply determine the public URL that will allow
bridged clients to access those files.

```
NETWORK1 -> (bytes or private URL) -> matterbridge -> mediaserver
                                                   -> NETWORK2 (URL to the mediaserver)
```

## Local folder

If you already have a folder that's served by a web server on the same machine,
you can simply configure matterbridge to place attachments in that folder, and
to produce URLs allowing clients to reach that web server.

The final path of files is deterministic, in the format of `SHA1_HASH/FILENAME`.

For example, if the `/var/www/public` directory is served as `https://example.com/public/`,
you can configure matterbridge like so:

```
[general]
MediaDownloadPath="/var/www/public"
MediaServerDownload="https://example.com/public"
```

The matterbridge process needs to have write permissions to `MediaDownloadPath`
folder, but the web server only needs read permissions. Unless some other
program is using the same folder, you can define the permissions, assuming
your matterbridge daemon runs as a `matterbridge` user, like so:

```bash
mkdir -p /var/www/public
chown matterbridge: /var/www/public
chmod 755 /var/www/public
```

### Running a web server

Running a static web server is beyond the scope of this documentation, but for
testing purposes (and only for testing purposes), you may simply run:

```bash
cd /var/www/public
python3 -m http.server 8080
```

Then use `MediaServerDownload="http://localhost:8080"` in your `matterbridge.toml`.

If you're looking for a web server to run, take a look at [ferron](https://github.com/ferronweb/ferron), [caddy](https://caddyserver.com/), [nginx](https://www.f5.com/products/nginx#products), or [apache](https://httpd.apache.org/).

### Garbage collection

matterbridge will not by default delete older files, a process known as [garbage collection](https://en.wikipedia.org/wiki/Garbage_collection_(computer_science)). If you have a lot of storage, it's not really a problem, and will help keep archived links functional. However, if you have limited storage, or want links to expire after a certain amount of time, you should setup an automated task deleting older files.

An example command achieving this is:

```bash
find /var/www/public -mindepth 1 -mtime +30 -delete
```

> [!WARNING]
> If you run this command, it will remove all older files and directories
> in `/var/www/public`. If that folder is used by other programs than matterbridge,
> you may want to select a specific sub-folder instead.

The `mindepth` argument avoids removing the top-level directory itself. The
`mtime` argument determines how many days to wait before deleting files.

How to run that command regularly depends on your particular system. In most
situations, you may use:

- a [systemd timer](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html?__goaway_challenge=meta-refresh&__goaway_id=a1be45bf816caf1b09559fb30b48080c&__goaway_referer=https%3A%2F%2Fduckduckgo.com%2F)
- a [cronjob](https://en.wikipedia.org/wiki/Cron)

Explaining this is beyond the scope of this documentation, but you may find
a lot of documentation and tutorials online about this.

## S3 protocol

matterbridge can upload media to [S3-compatible](https://en.wikipedia.org/wiki/Amazon_S3)
object stores. This is useful when you already rent S3 services from a hosting
provider, or when you already run your own.

If you have no idea what this means, S3 hosting is probably not for you. In
most cases, it's a lot more complex than serving files from a local folder as
explained in the previous section, and provides no advantage.

Sample matterbridge configuration:

```toml
[general]
# Public URL serving objects from the bucket, usually ends with bucket name.
MediaServerDownload="https://s3.example.com/matterbridge"
# S3 connection settings (common names used by many S3 clients)
S3Endpoint="http://s3.localhost:9000"
S3AccessKey="GKd2b5133b08831183839b78e4"
S3SecretKey="931701b8b56a268b6b9c51b530e67f32ba11731a40c8812893bce76bbdf2db68"
# Name of the S3 bucket where to store the files
S3Bucket = "matterbridge"
# Name of the S3 region. Needs to match the declared server region.
S3Region = "custom"
# Both MinIO and garage require S3ForcePathStyle=true.
# Do not disable it unless you know what you are doing.
S3ForcePathStyle=true
# To use presigned URLs instead of public buckets. Presigned URL will be valid for 7 days.
# when using this setting MediaServerDownload is ignored.
# Please note that this produces awful, long links.
S3Presign=false
```

> [!INFO]
> Note that the `S3Bucket` needs to be publicly readable. How to achieve this
> depends on your specific S3 implementation.

### Specific implementations

#### garage

With garage, the `S3Region`, `S3AccessKey` and `MediaServerDownload` require
specific attention.

The `S3Region` setting needs to match the configuration in `garage.toml`. By
default, use `S3Region = "garage"`.

The `S3AccessKey` setting uses the public key generated with the `garage key create`
command. Take for example this command run:

```bash
garage -c garage.toml key create test
==== ACCESS KEY INFORMATION ====
Key ID:              GKd2b5133b08831183839b78e4
Key name:            test
Secret key:          931701b8b56a268b6b9c51b530e67f32ba11731a40c8812893bce76bbdf2db68
Created:             2026-01-04 09:41:15.158 +01:00
Validity:            valid
Expiration:          never

Can create buckets:  false

==== BUCKETS FOR THIS KEY ====
Permissions  ID  Global aliases  Local aliases
```

Here we created a key named `test`, but we would use the key `Key ID` and
`Secret key` fields for matterbridge configuration:

```toml
S3AccessKey = "GKd2b5133b08831183839b78e4"
S3SecretKey = "931701b8b56a268b6b9c51b530e67f32ba11731a40c8812893bce76bbdf2db68"
```

In garage, bucket names such as `matterbridge.example.com` are domain names
which are served as both:

- absolute names: `matterbridge.example.com` if a reverse proxy serves
  the `s3_web` binding address/port directly over ports 80/443
- relative names: `matterbridge.example.com.garage.example.com` if the
  `s3_web` `root_domain` is `garage.example.com`

Note that if you use non-standard HTTP ports, you need to specify this in the
matterbridge configuration, like so:

```toml
S3Endpoint="http://localhost:3900"
MediaServerDownload="http://matterbridge.web.garage.localhost:3902"
```

In all cases, you need to run `garage bucket website --allow BUCKET_NAME` in
order to serve the bucket publicly. More information about serving static web
files from garage can be found [in the garage docs](https://garagehq.deuxfleurs.fr/documentation/cookbook/exposing-websites/).

Here's a complete example `matterbridge.toml` for a local testing garage:

```toml
[general]
S3Endpoint="http://localhost:3900"
S3AccessKey="GK98afb45996b7f4091acd8374"
S3SecretKey="baa3ecab309b0b3b3b73c524076a9d33aa61e08988f7cc953dc84a95917efbc4"
S3ForcePathStyle=true
S3Bucket="matterbridge"
S3Region="garage"
MediaServerDownload="http://matterbridge.web.garage.localhost:3902"
```

### minio

> [!WARNING]
> minio is governed by a for-profit entity which decisions with major impacts
> very lightly, such as recently with the [Docker CVE scandal](https://github.com/minio/minio/issues/21647?trk=public_post_comment-text).
> It is recommended not to use it until the project establishes proper
> governance, and you should definitely not use the Docker images which
> may contain security vulnerabilities.

On a fresh minio install, the default credentials are:

```toml
S3AccessKey="minioadmin"
S3SecretKey="minioadmin"
```