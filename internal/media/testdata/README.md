# Image conversion fixtures

These synthetic 8x4 red/blue blocks were generated for AICE, not copied from
an upstream repository. WebP files are checked in so Go tests need no image
converter. `blocks-alpha.webp` has two transparent columns on the left;
`blocks-animated.webp` alternates the opaque and transparent versions.

Reproduce with ImageMagick and cwebp (libwebp 1.5.0):

```sh
magick -size 8x4 xc:red -fill blue -draw 'rectangle 4,0 7,3' /tmp/aice-format-source.png
cwebp -quiet -lossless /tmp/aice-format-source.png -o blocks-lossless.webp
cwebp -quiet -q 90 /tmp/aice-format-source.png -o blocks-lossy.webp
magick -size 8x4 xc:none -fill red -draw 'rectangle 2,0 3,3' -fill blue -draw 'rectangle 4,0 7,3' /tmp/aice-format-alpha.png
cwebp -quiet -q 90 /tmp/aice-format-alpha.png -o blocks-alpha.webp
magick -delay 10 /tmp/aice-format-source.png -delay 10 /tmp/aice-format-alpha.png -loop 0 blocks-animated.webp
```

GIF and BMP inputs are generated in Go tests, including offset GIF frames,
malformed dimensions, unsupported BMP compression, and deleted source files.
