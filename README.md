<div align="center">
  <img src="./compacty-icon.png" width="256" height="64" alt=""/>
</div>

**compacty** is a configurable command-line tool that compresses files (currently only PNGs, JPEGs, GIFs by default) by running them through multiple compression tools at once, and picking the best result. Inspired by [picopt](https://github.com/ajslater/picopt/), [optimizt](https://github.com/343dev/optimizt/), [Trimage](https://github.com/Kilian/Trimage), and [ImageOptim](https://github.com/ImageOptim/ImageOptim).

## ...why?
Every tool and every file is unique. **Which tool that gives the best compression on one file might be different on another.**

compacty was born out of a need in Roblox game development. In Roblox, assets are downloaded every time a player joins a game. Finding the best possible compression for each asset is crucial to reduce load times. However, manually testing a file against multiple compression tools is tedious. **compacty automates this process.** It runs your files through multiple compression tools and finding the most optimal compression for you.

compacty was also my first real shot at learning Go. I enjoyed it.

## installation

### prerequisites!
> [!IMPORTANT]
> compacty is a meta-tool that runs other compression tools. **compacty does not include nor bundle these tools by itself.** You must get the tools you wish to use separately and ensure that:
> - They're available in your system's `PATH`, or
> - The binary is placed in the same directory as compacty.

The default configuration uses the following tools:
- [Efficient Compression Tool (ECT)](https://github.com/fhanau/Efficient-Compression-Tool/)
- [imagemagick](https://imagemagick.org/)
- [pingo](https://css-ig.net/pingo/) (for Windows. To run this on Linux and MacOS you would need to install `wine`)
- [oxipng](https://github.com/shssoichiro/oxipng/)
- [pngout](http://www.advsys.net/ken/utils.html)
- [pngquant](https://pngquant.org/)
- [jpegoptim](https://github.com/tjko/jpegoptim/)
- jpegtran (a JPEG manipulation tool provided by [libjpeg](https://jpegclub.org/reference/reference-sources/), [libjpeg-turbo](https://github.com/libjpeg-turbo/libjpeg-turbo), or [mozjpeg](https://github.com/mozilla/mozjpeg/))
- [gifsicle](http://www.lcdf.org/gifsicle/)

### from releases
1. Navigate to the [Releases](https://github.com/ArrayNone/compacty/releases/) page
2. Download the binary appropriate for your operating system (Windows, MacOS, Linux)

## usage examples

```bash
# Run by using the `default-preset` defined in your config file
compacty image.png

# Run by using a specific preset
compacty --preset=lossy-highquality imageA.png imageB.jpeg

# List all of your tools and presets from the config file
compacty --list

# Generate a `.tsv` report after compressing for further analysis (`.tsv` report separated for each file format)
compacty --report imageA.png ./Pictures/imageB.jpeg # Generates report.png.tsv and ./Pictures/report.jpeg.tsv 

# [EXPERIMENTAL] Measure the decoding time for each compression result using Go's native binaries. Only PNGs, JPEGs, and GIFs are supported.
# (use `--keep-all` to save the results that have the fastest decode time)
compacty --decode-time imageA.png
```

Run `compacty --help` to see all available flags.

## configuration
compacty uses a `config.yaml` file that defines tools, presets, etc. On first run, a default configuration will be created in your user's config directory:
- Windows: `%APPDATA%\compacty\config.yaml`
- Linux: `~/.config/compacty/config.yaml`
- macOS: `~/Library/Application Support/compacty/config.yaml`

You can see the default configuration at [defaultconfig.go](./internal/config/defaultconfig.go). A more complete configuration is available at [complete-config.yaml](./complete-config.yaml) that contains even more tools, ready to be copy-pasted.

## licensing
compacty is licensed under the MIT License. See the [LICENSE](./LICENSE) file for more information.
