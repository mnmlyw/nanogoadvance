package platform

// Ebiten Kage shaders ported from
// upstream/NanoBoyAdvance/src/platform/core/src/device/shader/*.glsl.hh.
//
// Kage notes:
//   - Coordinates are in source-image pixels (Ebiten default since 2.5).
//   - imageSrc0At(uv) samples the first bound source image; bilinear vs
//     nearest is controlled by DrawRectShaderOptions.Filter — NOT the
//     shader itself.
//   - Multi-texture passes use imageSrc1At, etc. The bound image MUST
//     match the destination size's coordinate system (we keep all
//     intermediates at the same dimensions where possible).

// shaderColorAGB ⇄ color_agb.glsl — Pokefan531's color mangler ported
// from GBA-LCD characteristics. Single-pass per-pixel transform.
const shaderColorAGB = `
//kage:unit pixels
package main

func Fragment(_ vec4, src vec2, _ vec4) vec4 {
	const target_gamma  float = 2.2
	const display_gamma float = 2.2
	const darken_screen float = 1.0
	const sat           float = 1.0
	const lum           float = 0.94
	const contrast      float = 1.0
	const r  float = 0.82
	const g  float = 0.665
	const b  float = 0.73
	const rg float = 0.125
	const rb float = 0.195
	const gr float = 0.24
	const gb float = 0.075
	const br float = -0.06
	const bg float = 0.21

	screen := pow(imageSrc0At(src), vec4(target_gamma+darken_screen))
	avglum := vec4(0.5)
	screen = mix(screen, avglum, (1.0 - contrast))

	color := mat4(
		r,   rg,  rb,  0.0,
		gr,  g,   gb,  0.0,
		br,  bg,  b,   0.0,
		0.0, 0.0, 0.0, 0.0,
	)
	adjust := mat4(
		(1.0-sat)*0.3086+sat, (1.0-sat)*0.3086,     (1.0-sat)*0.3086,     1.0,
		(1.0-sat)*0.6094,     (1.0-sat)*0.6094+sat, (1.0-sat)*0.6094,     1.0,
		(1.0-sat)*0.0820,     (1.0-sat)*0.0820,     (1.0-sat)*0.0820+sat, 1.0,
		0.0,                  0.0,                  0.0,                  1.0,
	)
	color *= adjust
	screen = clamp(screen*lum, 0.0, 1.0)
	screen = color * screen

	out := pow(screen, vec4(1.0/display_gamma))
	out.a = 1.0
	return out
}
`

// shaderColorHigan ⇄ color_higan.glsl — higan emulator's GBA color
// profile. Simpler per-pixel matrix.
const shaderColorHigan = `
//kage:unit pixels
package main

func Fragment(_ vec4, src vec2, _ vec4) vec4 {
	color := imageSrc0At(src)
	color = vec4(pow(color.rgb, vec3(4.0)), color.a)

	rgb := vec3(
		1.000*color.r + 0.196*color.g,
		0.039*color.r + 0.901*color.g + 0.117*color.b,
		0.196*color.r + 0.039*color.g + 0.862*color.b,
	)
	return vec4(pow(rgb, vec3(1.0/2.2)), 1.0)
}
`

// shaderLCDGhosting ⇄ lcd_ghosting.glsl — 50/50 blend of current frame
// with the previous frame, simulating the GBA LCD's slow pixel
// response. Needs 2 bound images: src0 = current, src1 = history.
const shaderLCDGhosting = `
//kage:unit pixels
package main

func Fragment(_ vec4, src vec2, _ vec4) vec4 {
	curr := imageSrc0At(src)
	hist := imageSrc1At(src)
	return mix(curr, hist, 0.5)
}
`

// shaderLCD1x ⇄ lcd1x.glsl — subpixel-grid effect (darkens pixel
// borders so the LCD grid is visible). Pure spatial; applied at the
// OUTPUT resolution, samples the source at native 240x160 coordinates.
//
// OutputSize uniform = the final draw target's size in pixels.
const shaderLCD1x = `
//kage:unit pixels
package main

var OutputSize vec2

func Fragment(dst vec4, _ vec2, _ vec4) vec4 {
	const PI float = 3.141592653589793
	const BRIGHTEN_SCANLINES float = 16.0
	const BRIGHTEN_LCD       float = 24.0
	inputSize := vec2(240.0, 160.0)
	uv := dst.xy / OutputSize
	angle := 2.0 * PI * ((uv * inputSize) - 0.25)
	yf := (BRIGHTEN_SCANLINES + sin(angle.y)) / (BRIGHTEN_SCANLINES + 1.0)
	xf := (BRIGHTEN_LCD + sin(angle.x)) / (BRIGHTEN_LCD + 1.0)
	srcPx := uv * imageSrc0Size()
	col := imageSrc0At(srcPx).rgb
	return vec4(yf*xf*col, 1.0)
}
`

// Note: sharp_bilinear is implemented in filters.go without a shader
// pass — Ebiten's built-in nearest-scale-to-integer-multiple followed
// by linear-fit-to-window produces the same visual result as upstream's
// sharp_bilinear.glsl shader.
