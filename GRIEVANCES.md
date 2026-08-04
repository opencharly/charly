# Four grievances

Every tool here exists because something in ordinary DevOps practice is worse than it needs to be.
This document names four of those things, states the status quo without strawmanning it, and shows
what OpenCharly does instead — with the mechanism, not the marketing.

`VISION.md` says where the project is going. This says what it is running away from.

---

## 1. Configuration is written per-artifact, so nothing composes

### The status quo

A Containerfile describes one image. If two images need the same three tools, you write those tools
twice, or you invent a base image and inherit — which couples the two images to a shared ancestor
they may not otherwise want, and makes every change to the ancestor a change to both.

The usual escapes each cost something:

| Escape | What it costs |
|---|---|
| A shared base image | Inheritance is a chain, not a set. You get exactly one parent, and everything in it. |
| Multi-stage `COPY --from` | Works for build artifacts, not for "install this package and its config and its service". |
| A shell script both images run | The script is opaque to the build cache, to the linter, and to anything trying to answer "what is in this image?". |
| Templating (Jinja, Helm, `envsubst`) | You are now maintaining a program that emits configuration, and the errors move from build time to render time. |

None of these is wrong. They are what you do when the unit of configuration is the *artifact*.

### What OpenCharly does

The unit of configuration is a **candy** — one concern, in one directory, with its own packages,
files, services and acceptance checks. A **box** is a candy that names a `base:` and a list of other
candies. Composition is a set, not a chain.

```yaml
tutorial-shell:
    candy:
        base: fedora
        candy:
            - '@github.com/opencharly/charly/candy/ripgrep:v2026.201.0706'
            - '@github.com/opencharly/charly/candy/sshd:v2026.201.0706'
```

Any box may compose any subset of this repository's candies, in any
combination, without either candy knowing the other exists.

**Why that is more than tidiness.** Because a candy is a unit, it can carry its own acceptance
tests, and those tests travel with it into every box that composes it. `ripgrep`'s checks — the
binary exists, `rg --version` parses, a pattern matches, an absent pattern exits 1 — run inside
*every* image that includes `ripgrep`, without that image's author writing anything. A shared base
image cannot do this; a shell script certainly cannot.

### The honest limits

Composition order still matters when two candies write the same file, and `require:` exists for
genuine prerequisites. Candies are not hermetic: two of them can install conflicting packages, and
you will find out at build time. This buys you composability, not commutativity.

---

## 2. The same software has to be described once per distribution

### The status quo

`apt-get install ripgrep` and `dnf install ripgrep` differ by more than a verb. Package names drift
(`python3-gobject` vs `python-gobject`), some packages exist in one ecosystem and not another, and
the init system, the default user, and the filesystem layout all vary underneath.

So a project that supports two distributions usually ends up with:

- two Containerfiles that have diverged more than anyone intended, or
- one Containerfile with `if [ -f /etc/debian_version ]` branches in shell, or
- a configuration-management layer (Ansible, Chef) whose entire purpose is papering over this, run
  as a separate system with its own execution model.

The third is the honest solution and it is a large dependency to take on for the problem of "the
package is called something else over there".

### What OpenCharly does

A candy declares packages once. If the name is the same everywhere, that is the whole declaration:

```yaml
ripgrep:
    candy:
        package:
            - ripgrep
```

That candy already works on Fedora, Arch, Debian and Ubuntu. Nothing further is written.

When names genuinely differ, a `distro:` map overrides *only* the part that differs:

```yaml
a11y-tools:
    candy:
        distro:
            arch:
                package: [python-atspi, python-gobject]
            fedora:
                package: [python3-pyatspi, python3-gobject]
```

First matching section wins, and the distro identity is inherited down the base chain, so a box on
`fedora` selects the `fedora` section without stating it.

**The same principle covers init systems**, which is where the difference usually hurts most. A
candy declares a `service:`; charly resolves which init the *target* uses and injects that init's
own candy into the composition — `supervisord` for a container image, nothing extra for a machine
that already runs systemd. The candy never names an init, and the same candy is correct on both.

### The honest limits

"No or minimal changes" is a real claim, not a universal one. A package that exists only on one
distribution is still a per-distro problem, and a candy that shells out to `systemctl` directly is
still writing for one init. The claim is that the *common* cases — package naming, service
declaration, user identity — are absorbed, so the remaining per-distro surface is small and
explicit rather than diffuse.

---

## 3. Container tooling and VM tooling are separate worlds

### The status quo

You describe a container with a Containerfile. You describe a VM guest with Packer, or cloud-init,
or a Kickstart file, or a golden image someone built by hand two years ago. You describe a
configured host with Ansible.

These are three different languages, three different execution models, and three different
definitions of "done". Worse, they are usually maintained by different people, so the container
image and the VM image drift — and the drift is invisible until something behaves differently in
one of them.

The standard answer is "use containers for everything". That answer is unavailable the moment you
need a different kernel, nested virtualization, a device that will not pass through, or a full
systemd that is not worth fighting.

### What OpenCharly does

The same candy list is consumed by every substrate. `pod:`, `vm:`, `local:`, `k8s:` and `android:`
all compile to one shared **InstallPlan IR** — a single intermediate representation that a target
then executes in its own idiom.

```yaml
# a container
my-app:
    pod:
        image: my-app-box

# the same candies on a machine, over SSH
my-app-guest:
    vm:
        from: my-app-box
```

Changing the substrate keyword is the whole change. Charly handles what differs underneath: which
init to install, whether packages go into a layer or onto a running system, whether a service
becomes a supervisord program or a systemd unit, how ports are published.

**Why an IR rather than a translation layer.** Because a translation layer converts A into B and
inherits the quirks of both. A shared IR means neither container-shaped nor VM-shaped is the
"real" one — a new substrate is a new target consuming the same plan, not another pairwise
conversion.

### The honest limits

Not everything is substrate-neutral, and pretending otherwise would be the same lie the status quo
tells. A `local:` deploy touches a real machine's packages and units, which is why this
repository's own examples run it inside a disposable VM guest rather than against a workstation. A
VM has a kernel and a boot sequence a container does not. What is portable is the *composition*;
the substrates remain genuinely different, and charly's job is to make the difference explicit and
handled rather than duplicated across three toolchains.

---

## 4. Isolation is opt-in, shallow, and stops one level down

### The status quo

Two habits, both understandable, both bad:

**Running as root because it is easier.** Rootless containers have real friction — uid mapping,
subuid ranges, storage drivers, and a long tail of images that assume uid 0. So a great deal of
tooling still runs privileged, and the isolation you believe you have is thinner than advertised.

**Nesting that does not work.** Build a container that itself builds containers, and you meet
`--privileged`, or a bind-mounted docker socket — which is a root shell on the host wearing a
costume. Run a VM inside a container, or a container inside a VM guest, and the usual answer is
"don't".

The consequence is that the moment you want a genuinely disposable environment — one an agent can
be handed, one that can be destroyed and rebuilt without thought — you find the isolation boundary
is either too weak to trust or too rigid to nest.

### What OpenCharly does

Every box is designed to run **rootless and nested**. The `container-nesting` candy exists
specifically so that rootless podman, buildah and skopeo work *inside a rootless outer container*,
at the default uid 1000, with no added capabilities. Rootless libvirt (`virtqemud`,
`virtnetworkd`) runs as ordinary supervisord programs at uid 1000, keyed off `$XDG_RUNTIME_DIR`,
needing only `/dev/kvm`.

That is what makes **candyboxing** possible: a box can run as a VM inside a container, or as a
container inside a VM guest, and charly's own acceptance beds do both. `check-builder-vm`,
`check-substrate`, `check-group-vm` and `check-structkind-vm` all nest a real VM inside the test
environment; `openclaw-desktop` runs nested charly inside a container.

**Why this is the load-bearing one.** It is what lets the security model be *"secure the box, not
the candy"*. If the boundary is a real kernel-enforced one, and if it nests, then you can hand an
agent the entire toolset inside a disposable environment instead of trying to whitelist which
commands it may run. Command allowlists fail because the interesting attacks are compositions of
boring commands. A wall does not care.

### The honest limits

Rootless nesting requires kernel support (user namespaces, and `/dev/kvm` for the VM case) and it
is not free — there are performance costs and some device classes genuinely cannot pass through.
"Designed to" is not "does, on every host, unconditionally". The claim is that nesting and
rootlessness are the *default assumption* the boxes are built against and continuously tested on,
rather than an exotic configuration you are on your own with.

---

## What this adds up to

Each grievance is separately survivable. Together they produce the thing this project is actually
reacting to: an environment nobody can reproduce, described in four languages, that only one person
can rebuild, and that nobody dares hand to an automated agent because the isolation is not trusted.

The four answers compose into one property — **a described environment that is disposable**. You
can destroy it because you can rebuild it; you can rebuild it because the description is complete;
the description is complete because it is composed of units that each carry their own proof.

For the positive statement of where that leads, see [VISION.md](VISION.md). For how any of it
works, see the skills index in [plugins/README.md](plugins/README.md) or the site at
[opencharly.ai](https://opencharly.ai).
