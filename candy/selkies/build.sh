#!/bin/bash
# Selkies build script — runs in pixi builder stage.
# Builder image provides: gcc, nodejs, npm, python3-devel, etc.
set -euo pipefail

# 1. Install selkies Python package (C extensions: evdev, xkbcommon)
cd /tmp
curl -sL https://github.com/selkies-project/selkies/archive/af1a1c252563d2f136d641b81d6b1dd38a3a0d93.tar.gz | tar xzf -
cd selkies-*
sed -i '/"av>/d' pyproject.toml
sed -i '/cryptography/d' pyproject.toml

# Patch keyboard input for generic XKB layout support (not US-only):
# - Read XKB_DEFAULT_* from env instead of hardcoding US layout
# - Scan level 2 (AltGr) keys into scancode map
# - Add AltGr modifier check for level-2 key injection
# - Remove Latin-1 and Euro bypass so all chars use scancode map
python3 -c "
import re

f = 'src/selkies/input_handler.py'
with open(f) as fh:
    code = fh.read()

# 1. Read XKB layout from env instead of hardcoding US
code = code.replace(
    'rules=\"evdev\", model=\"pc105\", layout=\"us\", variant=\"\", options=\"\"',
    'rules=os.environ.get(\"XKB_DEFAULT_RULES\",\"evdev\"), model=os.environ.get(\"XKB_DEFAULT_MODEL\",\"pc105\"), layout=os.environ.get(\"XKB_DEFAULT_LAYOUT\",\"us\"), variant=os.environ.get(\"XKB_DEFAULT_VARIANT\",\"\"), options=os.environ.get(\"XKB_DEFAULT_OPTIONS\",\"\")'
)

# 2. Remove Latin-1 + Euro bypass — all chars go through scancode map
code = code.replace(
    'if (0xA0 <= keysym <= 0xFF) or keysym == 0x20AC or ((keysym & 0xFF000000) == 0x01000000):',
    'if (keysym & 0xFF000000) == 0x01000000:'
)

# 3. Add level 2 (AltGr) scanning to _build_wayland_keymap
# Find the end of level 1 scanning block and add level 2 after it
level1_block = '''            syms_1 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 1)
            if syms_1:
                for sym in syms_1:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        if sym not in level_0_keys:
                            self.wayland_shift_required_keys.add(sym)'''

level2_addition = '''            syms_1 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 1)
            if syms_1:
                for sym in syms_1:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        if sym not in level_0_keys:
                            self.wayland_shift_required_keys.add(sym)

            syms_2 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 2)
            if syms_2:
                for sym in syms_2:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        self.wayland_altgr_required_keys.add(sym)'''

code = code.replace(level1_block, level2_addition)

# 4. Initialize wayland_altgr_required_keys alongside shift_required_keys
code = code.replace(
    'self.wayland_shift_required_keys = set()',
    'self.wayland_shift_required_keys = set()\\n        self.wayland_altgr_required_keys = set()'
)

# 5. For AltGr keys: inject AltGr + scancode directly via pixelflux (not wtype)
# The browser sends AltGr as momentary press/release, so active_modifiers is empty
# when the character arrives. Direct injection through the same input device avoids
# cross-device race conditions between pixelflux and wtype.
inject_block = '''            if scancode:
                try:
                    self.wayland_input.inject_key(scancode, 1 if down else 0)'''

altgr_inject = '''            if scancode is not None and hasattr(self, 'wayland_altgr_required_keys') and keysym in self.wayland_altgr_required_keys:
                if 65027 not in self.active_modifiers:
                    altgr_sc = self.wayland_scancode_map.get(0xfe03)
                    if altgr_sc:
                        try:
                            if down:
                                self.wayland_input.inject_key(altgr_sc, 1)
                                self.wayland_input.inject_key(scancode, 1)
                                self.wayland_input.inject_key(scancode, 0)
                                self.wayland_input.inject_key(altgr_sc, 0)
                            return
                        except Exception as e:
                            logger_webrtc_input.warning(f\"AltGr inject failed: {e}\")

            if scancode:
                try:
                    self.wayland_input.inject_key(scancode, 1 if down else 0)'''

code = code.replace(inject_block, altgr_inject)

with open(f, 'w') as fh:
    fh.write(code)

print('Patched input_handler.py: env-based layout, level 2 AltGr scanning, modifier checks')
"

# Patch selkies.py to reuse a single pixelflux ScreenCapture() per display_id
# across reconfigure_displays cycles.
#
# Root cause: pixelflux_wayland's WaylandBackend has no Drop impl and its
# run_wayland_thread runs event_loop.run(None, ...) with no exit path — the
# CalloopEvent::Closed handler is empty (lib.rs:928). Upstream selkies
# creates a fresh ScreenCapture() on every _start_capture_for_display call,
# so every user-triggered reconfigure (framerate/resolution/encoder change)
# leaks an entire Wayland compositor AppState — Space, ShmState, GBM device,
# GLES renderer, offscreen Dmabuf, frame_buffer Vec<u8> — into the container
# cgroup, showing up as memfd-backed shmem that can only be reclaimed by
# destroying the cgroup (container restart).
#
# Observed live: ~3.67 GB shmem accumulated over 48 minutes of active
# streaming under CPU JPEG encoder backlog on a 3058x1604@60 session,
# matching ~60 leaked compositor instances at ~50 MB each.
#
# Fix: cache ScreenCapture per display_id on self, so stop_capture/start_capture
# cycles reuse the same Rust thread instead of leaking a new one each time.
python3 -c "
f = 'src/selkies/selkies.py'
with open(f) as fh:
    code = fh.read()

needle = '            capture_module = ScreenCapture()\n'
replacement = (
    '            if not hasattr(self, \"_shared_screen_captures\"):\n'
    '                self._shared_screen_captures = {}\n'
    '            if display_id not in self._shared_screen_captures:\n'
    '                self._shared_screen_captures[display_id] = ScreenCapture()\n'
    '            capture_module = self._shared_screen_captures[display_id]\n'
)

if needle not in code:
    raise SystemExit('FATAL: could not locate ScreenCapture() instantiation in selkies.py — upstream changed?')
if code.count(needle) != 1:
    raise SystemExit('FATAL: ScreenCapture() instantiation is not unique in selkies.py — patch ambiguity')

code = code.replace(needle, replacement)

with open(f, 'w') as fh:
    fh.write(code)

print('Patched selkies.py: _shared_screen_captures cache prevents pixelflux WaylandBackend leak on reconfigure')
"

# ---------------------------------------------------------------------------
# Build patched pixelflux from source BEFORE selkies pip install.
#
# ROOT CAUSE: Smithay's GlesRenderer caches DMA-BUF imports in
# HashMap<WeakDmabuf, GlesTexture> with no automatic eviction. The cache is
# only cleaned when the compositor calls GlesRenderer::cleanup_texture_cache()
# explicitly. Pixelflux never calls it. Every dmabuf Chrome commits (60fps
# under active streaming) creates an EGL image via eglCreateImageKHR and
# caches a GlesTexture + mmap region through /dev/dri/renderD128. Over 20 min
# of real streaming on charly-selkies-desktop-82.23.94.69, /proc/<pid>/maps
# showed 1528 cached DRM mappings = 9 GB virtual address space and ~1.5 GB
# cgroup shmem, with only 19 MB file_mapped. 12 OOM kills logged on that
# instance.
#
# FIX: call renderer.cleanup_texture_cache() at the start of each render
# frame in run_wayland_thread. This runs the existing
# dmabuf_cache.retain(|k,_| !k.is_gone()) pass, which evicts entries whose
# server-side Dmabuf Arc has been dropped by Chrome's wl_buffer.destroy.
# The eviction cascades: GlesTexture drop -> EGL image drop -> dmabuf fd
# close -> amdgpu GEM BO release -> shmem pages freed.
#
# Build-time prerequisite: fedora-builder now includes rpmfusion + rust +
# codec dev libs so cargo can compile pixelflux_wayland. The only patch we
# MUST also make is removing nvenc-sys from Cargo.toml and stubbing
# encoders/nvenc.rs — we do not have CUDA headers in the builder, and the
# target container has an AMD iGPU anyway (no NVENC at runtime).
# ---------------------------------------------------------------------------
PFX_SHA=9650b0380f248364560ac872c6356d807e5de41b
cd /tmp
curl -sL "https://github.com/opencharly/pixelflux/archive/${PFX_SHA}.tar.gz" | tar xzf -
cd "pixelflux-${PFX_SHA}"

# NVENC (Patch 2a/2b) is stubbed UNLESS the builder ships CUDA + the NVENC
# headers. The cuda-arch-builder (used by the GPU VM / cachyos-gpu path) has
# nvcc + ffnvcodec's nvEncodeAPI.h, so pixelflux builds the real nvenc-sys
# encoder there; the stock arch/fedora builder has no CUDA, so the stub keeps
# the container selkies-desktop build green. One build.sh, capability-driven
# (R3) — no per-image fork.
PIXELFLUX_NVENC=0
if command -v nvcc >/dev/null 2>&1 || [ -e /opt/cuda/bin/nvcc ] || { [ -n "${CUDA_HOME:-}" ] && [ -d "${CUDA_HOME}/include" ]; }; then
    if [ -e /usr/include/ffnvcodec/nvEncodeAPI.h ] || [ -e /usr/include/nvEncodeAPI.h ] || [ -e /opt/cuda/include/nvEncodeAPI.h ]; then
        PIXELFLUX_NVENC=1
    fi
fi
if [ "$PIXELFLUX_NVENC" = "1" ]; then
    echo "pixelflux: CUDA + NVENC headers detected — building real NvencEncoder (GPU encode)"
# Patch 2d: fix NvencEncoder::new() for Ada/Lovelace (RTX 40xx) + recent drivers.
# Upstream pixelflux fetches the encode config with the LEGACY named preset
# NV_ENC_PRESET_LOW_LATENCY_HQ_GUID via nvEncGetEncodePresetConfig AND discards
# its return value. That named preset was deprecated in NVENC SDK 10 and on Ada
# GPUs under driver 6xx no longer yields a valid config — preset_config stays
# zeroed, so nvEncInitializeEncoder rejects it ("Failed to initialize encoder")
# and pixelflux silently falls back to CPU x264 (the exact silent-fallback the
# selkies-encoder eval now guards against). ffmpeg's h264_nvenc encodes fine on
# the same GPU because it uses the modern P-preset + tuningInfo Ex API. Switch
# pixelflux to nvEncGetEncodePresetConfigEx(P4, LOW_LATENCY) + init tuningInfo,
# CHECK both NVENCSTATUS returns, and surface the code so any future failure is
# diagnosable instead of silent. nvenc-sys (NVENCAPI 11.1, the pinned dep)
# already exposes the Ex fn, the P-preset GUIDs, NV_ENC_TUNING_INFO, and the
# init tuningInfo field — no dep bump needed.
python3 - pixelflux_wayland/src/encoders/nvenc.rs <<'NVENC_PRESET_FIX_EOF'
import sys, pathlib
p = pathlib.Path(sys.argv[1])
code = p.read_text()

# A: legacy preset fetch -> modern Ex (P4 + low-latency tuning), return CHECKED
old_a = '''            let get_preset = function_list.nvEncGetEncodePresetConfig.unwrap();
            get_preset(
                encoder_session,
                NV_ENC_CODEC_H264_GUID,
                NV_ENC_PRESET_LOW_LATENCY_HQ_GUID,
                &mut preset_config,
            );'''
new_a = '''            let tuning_info = NV_ENC_TUNING_INFO::NV_ENC_TUNING_INFO_LOW_LATENCY;
            let get_preset = function_list.nvEncGetEncodePresetConfigEx.unwrap();
            let preset_status = get_preset(
                encoder_session,
                NV_ENC_CODEC_H264_GUID,
                NV_ENC_PRESET_P4_GUID,
                tuning_info,
                &mut preset_config,
            );
            if preset_status != NVENCSTATUS::NV_ENC_SUCCESS {
                return Err(format!(
                    "Failed to get NVENC preset config (P4/low-latency): NVENCSTATUS {}",
                    preset_status as i32
                ));
            }'''
if code.count(old_a) != 1:
    raise SystemExit('FATAL: pixelflux nvenc.rs preset-fetch anchor not unique (found %d)' % code.count(old_a))
code = code.replace(old_a, new_a)

# B1: init_params presetGUID -> P4 + carry tuningInfo
old_b1 = '''                presetGUID: NV_ENC_PRESET_LOW_LATENCY_HQ_GUID,
                encodeWidth: width,'''
new_b1 = '''                presetGUID: NV_ENC_PRESET_P4_GUID,
                tuningInfo: tuning_info,
                encodeWidth: width,'''
if code.count(old_b1) != 1:
    raise SystemExit('FATAL: pixelflux nvenc.rs init-params anchor not unique (found %d)' % code.count(old_b1))
code = code.replace(old_b1, new_b1)

# B2: init call -> capture + surface the real NVENCSTATUS
old_b2 = '''            let init_fn = function_list.nvEncInitializeEncoder.unwrap();
            if init_fn(encoder_session, &mut init_params) != NVENCSTATUS::NV_ENC_SUCCESS {
                return Err("Failed to initialize encoder".into());
            }'''
new_b2 = '''            let init_fn = function_list.nvEncInitializeEncoder.unwrap();
            let init_status = init_fn(encoder_session, &mut init_params);
            if init_status != NVENCSTATUS::NV_ENC_SUCCESS {
                return Err(format!(
                    "Failed to initialize encoder: NVENCSTATUS {}",
                    init_status as i32
                ));
            }'''
if code.count(old_b2) != 1:
    raise SystemExit('FATAL: pixelflux nvenc.rs init-call anchor not unique (found %d)' % code.count(old_b2))
code = code.replace(old_b2, new_b2)

p.write_text(code)
print('Patched pixelflux nvenc.rs: modern Ex preset API (P4 + low-latency tuning), checked returns, NVENCSTATUS surfaced')
NVENC_PRESET_FIX_EOF
else
# Patch 2a: delete the [dependencies.nvenc-sys] block from Cargo.toml
python3 -c "
import pathlib
p = pathlib.Path('pixelflux_wayland/Cargo.toml')
code = p.read_text()
old = '''[dependencies.nvenc-sys]
git = \"https://github.com/legion-labs/nvenc-sys\"
rev = \"996be4ceac8112e14ae127adcf8c699bcc1618f5\"
features = [\"cuda\"]
'''
if old not in code:
    raise SystemExit('FATAL: nvenc-sys block anchor not found in pixelflux Cargo.toml — upstream changed?')
p.write_text(code.replace(old, ''))
print('Patched pixelflux Cargo.toml: removed nvenc-sys dep (no CUDA headers in builder)')
"

# Patch 2b: replace encoders/nvenc.rs with a no-op stub.
# NvencEncoder::new always returns Err, so the GpuEncoder::Nvenc variant is
# never constructed at runtime. encode() and encode_raw() exist only to
# satisfy the enum variant's type signature — they are unreachable dead code.
cat > pixelflux_wayland/src/encoders/nvenc.rs << 'NVENC_STUB_EOF'
// Stubbed NVENC encoder — the real implementation needs nvenc-sys (CUDA),
// which this build does not include because fedora-builder does not ship
// CUDA headers and the target containers use AMD VA-API / software encoding.
// NvencEncoder::new() always returns Err so the GpuEncoder::Nvenc variant is
// never constructed; encode()/encode_raw() exist only to satisfy the type
// signatures referenced from lib.rs and are unreachable at runtime.

use std::ffi::c_void;
use smithay::backend::allocator::dmabuf::Dmabuf;

use crate::RustCaptureSettings;

pub struct NvencEncoder;

impl NvencEncoder {
    pub fn new(
        _settings: &RustCaptureSettings,
        _egl_display: *const c_void,
    ) -> Result<Self, String> {
        Err("NVENC encoder not available: nvenc-sys dep removed from pixelflux_wayland in this build".to_string())
    }

    pub fn encode(
        &mut self,
        _dmabuf: &Dmabuf,
        _frame_number: u64,
        _target_qp: u32,
        _force_idr: bool,
    ) -> Result<Vec<u8>, String> {
        Err("NVENC stub: encode() unreachable".to_string())
    }

    pub fn encode_raw(
        &mut self,
        _raw_data: &[u8],
        _frame_number: u64,
        _target_qp: u32,
        _force_idr: bool,
    ) -> Result<Vec<u8>, String> {
        Err("NVENC stub: encode_raw() unreachable".to_string())
    }
}
NVENC_STUB_EOF
echo "Stubbed pixelflux_wayland/src/encoders/nvenc.rs (NvencEncoder::new always Err)"
fi

# Patch 2b.5: disable the C++ X11 screen_capture_module build. pixelflux's
# setup.py BuildCtypesExt.run() calls build_custom_cpp() which invokes g++
# with -lX11 -lXext -lXfixes -ljpeg -lx264 -lyuv -lavcodec -lavutil against
# /usr/include/X11/*. We don't have libX11-devel in the builder (this is a
# Wayland-only image, Chrome runs under --ozone-platform=wayland) and
# pixelflux/__init__.py handles a missing screen_capture_module.so
# gracefully — _legacy_lib = None, everything falls through to the
# _GLOBAL_WAYLAND_BACKEND Rust path. Stub the build step to be a no-op.
python3 -c "
import pathlib
p = pathlib.Path('setup.py')
code = p.read_text()
old = '''    def run(self):
        super().run()
        self.build_custom_cpp()'''
new = '''    def run(self):
        super().run()
        # C++ X11 module build disabled — Wayland-only image. See selkies layer build.sh.
        pass'''
if code.count(old) != 1:
    raise SystemExit('FATAL: pixelflux setup.py build_custom_cpp anchor not unique (found %d)' % code.count(old))
p.write_text(code.replace(old, new))
print('Patched pixelflux setup.py: disabled C++ X11 screen_capture_module build')
"

# Patch 2c: call Renderer::cleanup_texture_cache() at the start of every
# render frame in run_wayland_thread. Anchor is the 2-line snippet
# starting each frame's closure:
#     let loop_start_time = Instant::now();
#     state.space.refresh();
python3 -c "
import pathlib
p = pathlib.Path('pixelflux_wayland/src/lib.rs')
code = p.read_text()
old = '''            let loop_start_time = Instant::now();
            state.space.refresh();
'''
new = '''            let loop_start_time = Instant::now();
            state.space.refresh();
            // Release EGL images / GL textures for Dmabufs whose server-side
            // Arc has been dropped (typical: Chrome called wl_buffer.destroy
            // after using the buffer in the previous frame). Without this,
            // smithay's GlesRenderer.dmabuf_cache grows unbounded because it
            // only evicts entries on explicit cleanup. Observed leak: 1528
            // cached imports, ~1.5 GB cgroup shmem, under 20 min of
            // 1604x3056@60 streaming on charly-selkies-desktop-82.23.94.69.
            if let Some(renderer) = state.gles_renderer.as_mut() {
                use smithay::backend::renderer::Renderer;
                let _ = renderer.cleanup_texture_cache();
            }
'''
if code.count(old) != 1:
    raise SystemExit('FATAL: pixelflux lib.rs render loop anchor not unique (found %d)' % code.count(old))
p.write_text(code.replace(old, new))
print('Patched pixelflux_wayland/src/lib.rs: cleanup_texture_cache() call per frame')
"

# Install the patched pixelflux into the pixi env. The upstream selkies
# pip install below will see pixelflux already satisfied.
"$HOME/.pixi/envs/default/bin/pip" install .

# Return to the selkies source directory (still lives at /tmp/selkies-<sha>/)
# so the following pip install targets the patched upstream selkies.
cd /tmp
rm -rf "/tmp/pixelflux-${PFX_SHA}"
cd selkies-*

"$HOME/.pixi/envs/default/bin/pip" install .

# 2. Build web UI dashboard (needs nodejs/npm from builder image)
cd addons/gst-web-core
npm install && npm run build
cd ../selkies-dashboard
cp ../gst-web-core/dist/selkies-core.js src/
npm install && npm run build

# 3. Stage web UI assets under $HOME (pixi copy_artifacts copies $HOME)
mkdir -p "$HOME/.local/share/selkies-build/web"
cp -r dist/* "$HOME/.local/share/selkies-build/web/"
cp ../gst-web-core/dist/selkies-core.js "$HOME/.local/share/selkies-build/web/src/" 2>/dev/null || true

# 4. Create NVRTC symlinks for pixelflux NVENC
NVRTC_DIR="$HOME/.pixi/envs/default/lib/python3.13/site-packages/nvidia/cu13/lib"
if [ -f "$NVRTC_DIR/libnvrtc.so.13" ] && [ ! -f "$NVRTC_DIR/libnvrtc.so" ]; then
    ln -s libnvrtc.so.13 "$NVRTC_DIR/libnvrtc.so"
    ln -s libnvrtc-builtins.so.13.2 "$NVRTC_DIR/libnvrtc-builtins.so" 2>/dev/null || true
fi

# Cleanup
cd /
rm -rf /tmp/selkies-*
