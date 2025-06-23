package config

func GetDefaultConfigStr() string {
	return `default-preset: default-args

mime-extensions:
  # For file formats that have multiple valid extensions (JPEG for example), you'll need to define them here so compacty can recognise them
  # See https://github.com/gabriel-vasile/mimetype/blob/master/supported_mimes.md for all available MIME types
  image/vnd.mozilla.apng: [".apng", ".png"] # image/apng is not supported
  image/png: [".png"]
  image/jpeg: [".jpg", ".jpeg", ".jfif"]
  image/gif: [".gif"]

wrappers:
  # Wrappers to use for running tools across different operating systems
  linux: # Source, or the running platform
    windows: wine # Wrapper to run for tools built for this platform
  windows:
    linux: wsl
  darwin:
    windows: wine

presets:
  # Presets are collection of arguments of a tool for a specific purpose (lossless compression, lossy compression, retaining transparency on images, etc.)
  # To change or add a tool's preset arguments, edit the tools' entries located way below

  default-args:
    description: Tools ran at their default settings with a few (if any) flags added. If not applicable, reasonable compression settings are used.
    shorthands: []
    default-tools:
      image/vnd.mozilla.apng: [oxipng, pingo]
      image/png: [oxipng, ect, pingo]
      image/jpeg: [jpegoptim, ect, pingo]
      image/gif: [gifsicle]

  lossless-loweffort:
    description: Lossless compression with fast, low effort compression settings.
    shorthands: [lossless-low, ll-low, lossless-fast, ll-fast]
    default-tools:
      image/vnd.mozilla.apng: [oxipng, pingo]
      image/png: [oxipng, ect, pingo]
      image/jpeg: [jpegoptim, jpegtran, ect, pingo]
      image/gif: [gifsicle]

  lossless-higheffort:
    description: Lossless compression with slow, high effort compression settings.
    shorthands: [lossless-high, ll-high, lossless-slow, ll-slow]
    default-tools:
      image/vnd.mozilla.apng: [oxipng, pingo]
      image/png: [oxipng, ect, pingo, pngout]
      image/jpeg: [jpegoptim, jpegtran, ect, pingo]
      image/gif: [gifsicle]

  lossless-maxbrute:
    description: Lossless compression with maximum (including bruteforce-y) effort compression settings. Extremely slow!
    shorthands: []
    default-tools:
      image/vnd.mozilla.apng: [oxipng, pingo]
      image/png: [oxipng, ect, pngout]
      image/jpeg: [jpegoptim, jpegtran, ect, pingo]
      image/gif: [gifsicle]

  image-keepalpha:
    description: Lossless image compression with high effort compression settings and fully transparent pixels (a = 0) retained.
    shorthands: [image-alpha, img-alpha]
    default-tools:
      image/vnd.mozilla.apng: [oxipng, pingo]
      image/png: [oxipng, ect, pingo]

  lossy-lowquality:
    # Images are typically compressed to at least 40 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in highly degraded output.
    shorthands: [lossy-low, ly-low]
    default-tools:
      image/vnd.mozilla.apng: []
      image/png: [pngquant]
      image/jpeg: [jpegoptim, imagemagick]
      image/gif: [gifsicle]

  lossy-subparquality:
    # Images are typically compressed to at least 50 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in greatly degraded output.
    shorthands: [lossy-subpar, ly-subpar]
    default-tools:
      image/vnd.mozilla.apng: []
      image/png: [pngquant]
      image/jpeg: [jpegoptim, imagemagick]
      image/gif: [gifsicle]

  lossy-midquality:
    # Images are typically compressed to at least 60 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in moderately degraded output.
    shorthands: [lossy-mid, ly-mid]
    default-tools:
      image/vnd.mozilla.apng: []
      image/png: [pngquant]
      image/jpeg: [jpegoptim, imagemagick]
      image/gif: [gifsicle]

  lossy-finequality:
    # Images are typically compressed to at least 70 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in lightly degraded output.
    shorthands: [lossy-fine, ly-fine]
    default-tools:
      image/vnd.mozilla.apng: []
      image/png: [pngquant]
      image/jpeg: [jpegoptim, imagemagick]
      image/gif: [gifsicle]

  lossy-highquality:
    # Images are typically compressed to at least 80 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in mildly degraded output.
    shorthands: [lossy-high, ly-high]
    default-tools:
      image/vnd.mozilla.apng: [pingo]
      image/png: [pingo, pngquant]
      image/jpeg: [jpegoptim, imagemagick, pingo]
      image/gif: [gifsicle]

  lossy-almostperfect:
    # Images are typically compressed to at least 90 score in the SSIMULACRA2 metric
    description: Lossy compression that typically results in output with almost no noticeable degradation.
    shorthands: [lossy-perfect, ly-perfect]
    default-tools:
      image/vnd.mozilla.apng: [pingo]
      image/png: [pingo]
      image/jpeg: [jpegoptim, imagemagick, pingo]
      image/gif: [gifsicle]

tools:
  # Define third-party compression tools here

  # Image compression tools, multiple formats
  ect:
    description: Lossless file compressor. https://github.com/fhanau/Efficient-Compression-Tool/
    command: ect
    platform: [windows, darwin, linux]
    supported-formats: [image/png, image/jpeg]
    output-mode: batch-overwrite
    arguments:
      default-args: []
      # Note: --mt-deflate speeds up processing considerably, but produces in a very slightly larger image (~0.1-0.2% more)
      lossless-loweffort: ["--mt-file", "--mt-deflate", "-2"] # ect does no compression at 1
      lossless-higheffort: ["--mt-file", "--mt-deflate", "-9"]
      lossless-maxbrute: ["--mt-file", "--mt-deflate", "-9", "--allfilters"]
      image-keepalpha: ["--mt-file", "--mt-deflate", "-9", "--strict"]
      # lossy-* omitted: Lossless only

  imagemagick:
    description: Image manipulation tool. PNG = Lossless compression. JPEG = Lossy compression. https://imagemagick.org/
    command: magick
    platform: [windows, darwin, linux]
    supported-formats: [image/png, image/jpeg]
    output-mode: batch-overwrite
    arguments:
      default-args: ["mogrify", "-define", "png:compression-level=9", "-quality", "90"]
      # Lossless compression is PNG only
      lossless-loweffort: ["mogrify", "-define", "png:compression-level=5", "-quality", "100"]
      lossless-higheffort: ["mogrify", "-define", "png:compression-level=9", "-quality", "100"]
      lossless-maxbrute: ["mogrify", "-define", "png:compression-level=9", "-quality", "100"]
      # image-keepalpha omitted: Does not support preserving fully transparent pixels
      # Lossy compression is JPEG only
      lossy-lowquality: ["mogrify", "-quality", "25"]
      lossy-subparquality: ["mogrify", "-quality", "35"]
      lossy-midquality: ["mogrify", "-quality", "50"]
      lossy-finequality: ["mogrify", "-quality", "70"]
      lossy-highquality: ["mogrify", "-quality", "85"]
      lossy-almostperfect: ["mogrify", "-quality", "100"]

  pingo:
    description: Lossless and lossy image compressor designed for web context. https://css-ig.net/pingo/
    command: pingo
    platform: [windows]
    supported-formats: [image/png, image/vnd.mozilla.apng, image/jpeg]
    output-mode: batch-overwrite
    arguments:
      default-args: []
      lossless-loweffort: ["-lossless", "-s1"]
      lossless-higheffort: ["-lossless", "-s4"]
      lossless-maxbrute: ["-lossless", "-s4"]
      image-keepalpha: ["-lossless", "-noalpha", "-s4"]
      # lossy-lowquality, lossy-subparquality, lossy-midquality, lossy-finequality omitted:
      # pingo can't get consistently below 80 SSIM2 score even at low -quality levels
      lossy-highquality: ["-s4", "-quality=90"]
      lossy-almostperfect: ["-s4", "-quality=95"]


  # PNG
  oxipng:
    description: Lossless PNG compressor. https://github.com/shssoichiro/oxipng/
    command: oxipng
    platform: [windows, darwin, linux]
    supported-formats: [image/png, image/vnd.mozilla.apng]
    output-mode: batch-overwrite
    arguments:
      default-args: ["--force"]
      lossless-loweffort: ["--force", "-o", "1", "-a"]
      lossless-higheffort: ["--force", "-o", "max", "-a"]
      lossless-maxbrute: ["--force", "-o", "max", "-a", "-Z", "--zi", "100"]
      image-keepalpha: ["--force", "-o", "max"] # Opt-out of -a
      # lossy-* omitted: Lossless only

  pngout:
    description: Lossless PNG compressor. http://www.advsys.net/ken/utils.html
    command: pngout
    platform: [windows, darwin, linux]
    supported-formats: [image/png]
    output-mode: input-output
    arguments:
      default-args: ["-force", "-y"]
      lossless-loweffort: ["-force", "-y", "-s3"]
      lossless-higheffort: ["-force", "-y", "-s1"]
      lossless-maxbrute: ["-force", "-y", "-s0"]
      # image-keepalpha omitted: Does not support preserving fully transparent pixels
      # lossy-* omitted: Lossless only

  pngquant:
    description: Lossy PNG compressor. https://pngquant.org/
    command: pngquant
    platform: [windows, darwin, linux]
    supported-formats: [image/png]
    output-mode: batch-overwrite
    arguments:
      default-args: ["--ext=.png", "--force"]
      # lossless-* omitted: Lossy only
      lossy-lowquality: ["--ext=.png", "--force", "--speed=1", "--quality=0-60"]
      lossy-subparquality: ["--ext=.png", "--force", "--speed=1", "--quality=0-70"]
      lossy-midquality: ["--ext=.png", "--force", "--speed=1", "--quality=0-80"]
      lossy-finequality: ["--ext=.png", "--force", "--speed=1", "--quality=0-90"]
      lossy-highquality: ["--ext=.png", "--force", "--speed=1", "--quality=0-100"]
      # lossy-almostperfect omitted: Can't consistently reach 90 SSIM2 at max quality score
      # image-keepalpha omitted: Does not support preserving fully transparent pixels


  # JPEG
  # JPEG does not support transparent pixels, no image-keepalpha
  jpegoptim:
    description: Lossless and lossy JPEG compressor. https://github.com/tjko/jpegoptim/
    command: jpegoptim
    platform: [windows, darwin, linux]
    supported-formats: [image/jpeg]
    output-mode: batch-overwrite
    arguments:
      default-args: ["--force"]
      lossless-loweffort: ["--force"]
      lossless-higheffort: ["--force"]
      lossless-maxbrute: ["--force"]
      lossy-lowquality: ["--force", "-m25"]
      lossy-subparquality: ["--force", "-m35"]
      lossy-midquality: ["--force", "-m55"]
      lossy-finequality: ["--force", "-m70"]
      lossy-highquality: ["--force", "-m90"]
      lossy-almostperfect: ["--force", "-m95"]

  # https://github.com/mozilla/mozjpeg/
  # https://github.com/libjpeg-turbo/libjpeg-turbo
  # https://jpegclub.org/reference/reference-sources/
  jpegtran:
    description: JPEG manipulation tool provided by libjpeg, libjpeg-turbo, or mozjpeg. Does lossless JPEG compression.
    command: jpegtran
    platform: [windows, darwin, linux]
    supported-formats: [image/jpeg]
    output-mode: stdout
    arguments:
      default-args: ["-optimize"]
      lossless-loweffort: ["-optimize"]
      lossless-higheffort: ["-optimize"]
      lossless-maxbrute: ["-optimize"]
      # lossy-* omitted: Lossless only


  # GIF
  gifsicle:
    description: GIF manipulation tool. Can compress GIFs losslessly and lossily. http://www.lcdf.org/gifsicle/
    command: gifsicle
    platform: [windows, darwin, linux]
    supported-formats: [image/gif]
    output-mode: batch-overwrite
    arguments:
      default-args: ["--batch", "--threads", "-O2"]
      lossless-loweffort: ["--batch", "--threads", "-O1"]
      lossless-higheffort: ["--batch", "--threads", "-O3"]
      lossless-maxbrute: ["--batch", "--threads", "-O3"]
      # image-keepalpha omitted: Does not support preserving fully transparent pixels.
      # -Okeepempty exists but it only keeps fully empty transparent *frames*, not pixels
      lossy-lowquality: ["--batch", "--threads", "-O3", "--lossy=80"]
      lossy-subparquality: ["--batch", "--threads", "-O3", "--lossy=50"]
      lossy-midquality: ["--batch", "--threads", "-O3", "--lossy=40"]
      lossy-finequality: ["--batch", "--threads", "-O3", "--lossy=20"]
      lossy-highquality: ["--batch", "--threads", "-O3", "--lossy=10"]
      lossy-almostperfect: ["--batch", "--threads", "-O3", "--lossy=2"]

`
}
