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

// ===== xBRZ =====
// Port of upstream xbrz.glsl.hh — a two-pass pixel-art upscaler.
// Algorithm credit chain: Hyllian (xBR), DeSmuME team (4xBRZ shader),
// quark-shaders (Freescale 2-pass), NBA (this fork).
//
// Pass 0 (shaderXBRZ0): per-pixel edge analysis. Examines a 5x5
//   neighborhood of the input and encodes blend decisions for the
//   four corners (E-F, E-H, E-D, E-B) into a packed float vec4
//   (later read back as info-map by pass 1).
// Pass 1 (shaderXBRZ1): consumes the info map plus the original
//   input, and at each output pixel decides how to blend up to two
//   neighboring pixels into a smoothed diagonal.
//
// Notes on the Kage port vs the GLSL original:
//   - `#define eq(a,b)` and `#define neq(a,b)` are now helper
//     functions returning float (1.0 / 0.0), because Kage has no
//     preprocessor and no GLSL-style vec3 == vec3 comparison.
//   - `#define P(x,y) texture(u_input_map, coord + stride*vec2(x,y))`
//     becomes `pix(coord, x, y)` and uses integer pixel offsets
//     directly (kage:unit pixels), since the source is 240x160.
//   - The GLSL mutating-`frag_color` accumulation pattern (write,
//     then `frag_color.z += 4.0` later) is restructured to accumulate
//     into a local `out vec4` and return once at the end.
//   - GLSL `mix(a, b, step(x,y))` patterns are preserved as-is —
//     Kage has the same `mix` and `step`.

const shaderXBRZ0 = `
//kage:unit pixels
package main

const blendNone     float = 0.0
const blendNormal   float = 1.0
const blendDominant float = 2.0

const luminanceWeight          float = 1.0
const equalColorTolerance      float = 30.0 / 255.0
const steepDirectionThreshold  float = 2.2
const dominantDirectionThreshold float = 3.6

const inputW float = 240.0
const inputH float = 160.0

// distYCbCr — perceptual color distance, BT.2020 weights.
func distYCbCr(pixA, pixB vec3) float {
	w := vec3(0.2627, 0.6780, 0.0593)
	scaleB := 0.5 / (1.0 - w.b)
	scaleR := 0.5 / (1.0 - w.r)
	diff := pixA - pixB
	Y := dot(diff, w)
	Cb := scaleB * (diff.b - Y)
	Cr := scaleR * (diff.r - Y)
	return sqrt((luminanceWeight*Y)*(luminanceWeight*Y) + Cb*Cb + Cr*Cr)
}

func isPixEqual(a, b vec3) bool { return distYCbCr(a, b) < equalColorTolerance }
func eqv(a, b vec3) bool        { return isPixEqual(a, b) }
func neqv(a, b vec3) bool       { return !isPixEqual(a, b) }

// pix samples the source at the integer (x, y) offset from coord,
// returning the RGB. coord is in atlas-absolute pixel space (i.e.
// already offset by imageSrc0Origin()).
func pix(coord vec2, x, y float) vec3 {
	return imageSrc0At(coord + vec2(x, y)).rgb
}

func Fragment(_ vec4, srcPos vec2, _ vec4) vec4 {
	// srcPos is the auto-interpolated source coord — already in
	// atlas-absolute pixel space (Ebiten internally packs every image
	// into a texture atlas, so coordinates need imageSrcNOrigin()
	// offsets). For pass 0 the source and dst are both 240x160, so
	// srcPos maps 1:1 to the native pixel we're processing.
	coord := srcPos

	A := pix(coord, -1, -1)
	B := pix(coord, 0, -1)
	C := pix(coord, 1, -1)
	D := pix(coord, -1, 0)
	E := pix(coord, 0, 0)
	F := pix(coord, 1, 0)
	G := pix(coord, -1, 1)
	H := pix(coord, 0, 1)
	I := pix(coord, 1, 1)

	// blendResult.{x,y,z,w} — one corner each (top-left, top-right,
	// bottom-right, bottom-left in xy-up GLSL coords).
	var br vec4

	// --- Corner z (bottom-right of E): pixels E F H I + neighbors
	if !((eqv(E, F) && eqv(H, I)) || (eqv(E, H) && eqv(F, I))) {
		distHF := distYCbCr(G, E) + distYCbCr(E, C) + distYCbCr(pix(coord, 0, 2), I) + distYCbCr(I, pix(coord, 2, 0)) + 4.0*distYCbCr(H, F)
		distEI := distYCbCr(D, H) + distYCbCr(H, pix(coord, 1, 2)) + distYCbCr(B, F) + distYCbCr(F, pix(coord, 2, 1)) + 4.0*distYCbCr(E, I)
		if distHF < distEI && neqv(E, F) && neqv(E, H) {
			if dominantDirectionThreshold*distHF < distEI {
				br.z = blendDominant
			} else {
				br.z = blendNormal
			}
		}
	}

	// --- Corner w (bottom-left of E): pixels D E G H + neighbors
	if !((eqv(D, E) && eqv(G, H)) || (eqv(D, G) && eqv(E, H))) {
		distGE := distYCbCr(pix(coord, -2, 1), D) + distYCbCr(D, B) + distYCbCr(pix(coord, -1, 2), H) + distYCbCr(H, F) + 4.0*distYCbCr(G, E)
		distDH := distYCbCr(pix(coord, -2, 0), G) + distYCbCr(G, pix(coord, 0, 2)) + distYCbCr(A, E) + distYCbCr(E, I) + 4.0*distYCbCr(D, H)
		if distGE > distDH && neqv(E, D) && neqv(E, H) {
			if dominantDirectionThreshold*distDH < distGE {
				br.w = blendDominant
			} else {
				br.w = blendNormal
			}
		}
	}

	// --- Corner y (top-right of E): pixels B C E F + neighbors
	if !((eqv(B, C) && eqv(E, F)) || (eqv(B, E) && eqv(C, F))) {
		distEC := distYCbCr(D, B) + distYCbCr(B, pix(coord, 1, -2)) + distYCbCr(H, F) + distYCbCr(F, pix(coord, 2, -1)) + 4.0*distYCbCr(E, C)
		distBF := distYCbCr(A, E) + distYCbCr(E, I) + distYCbCr(pix(coord, 0, -2), C) + distYCbCr(C, pix(coord, 2, 0)) + 4.0*distYCbCr(B, F)
		if distEC > distBF && neqv(E, B) && neqv(E, F) {
			if dominantDirectionThreshold*distBF < distEC {
				br.y = blendDominant
			} else {
				br.y = blendNormal
			}
		}
	}

	// --- Corner x (top-left of E): pixels A B D E + neighbors
	if !((eqv(A, B) && eqv(D, E)) || (eqv(A, D) && eqv(B, E))) {
		distDB := distYCbCr(pix(coord, -2, 0), A) + distYCbCr(A, pix(coord, 0, -2)) + distYCbCr(G, E) + distYCbCr(E, C) + 4.0*distYCbCr(D, B)
		distAE := distYCbCr(pix(coord, -2, -1), D) + distYCbCr(D, H) + distYCbCr(pix(coord, -1, -2), B) + distYCbCr(B, F) + 4.0*distYCbCr(A, E)
		if distDB < distAE && neqv(E, D) && neqv(E, B) {
			if dominantDirectionThreshold*distDB < distAE {
				br.x = blendDominant
			} else {
				br.x = blendNormal
			}
		}
	}

	// out starts as the blendResult, with additive flags later in z/w/y/x.
	out := br

	// Per-corner doLineBlend / haveShallowLine / haveSteepLine packing
	// (+4 for doLine, +16 for shallow, +64 for steep). Matches upstream
	// pass 0 lines 191-268 exactly.
	if br.z == blendDominant || (br.z == blendNormal &&
		!((br.y != blendNone && !isPixEqual(E, G)) || (br.w != blendNone && !isPixEqual(E, C)) ||
			(isPixEqual(G, H) && isPixEqual(H, I) && isPixEqual(I, F) && isPixEqual(F, C) && !isPixEqual(E, I)))) {
		out.z += 4.0
		distFG := distYCbCr(F, G)
		distHC := distYCbCr(H, C)
		if steepDirectionThreshold*distFG <= distHC && neqv(E, G) && neqv(D, G) {
			out.z += 16.0
		}
		if steepDirectionThreshold*distHC <= distFG && neqv(E, C) && neqv(B, C) {
			out.z += 64.0
		}
	}

	if br.w == blendDominant || (br.w == blendNormal &&
		!((br.z != blendNone && !isPixEqual(E, A)) || (br.x != blendNone && !isPixEqual(E, I)) ||
			(isPixEqual(A, D) && isPixEqual(D, G) && isPixEqual(G, H) && isPixEqual(H, I) && !isPixEqual(E, G)))) {
		out.w += 4.0
		distHA := distYCbCr(H, A)
		distDI := distYCbCr(D, I)
		if steepDirectionThreshold*distHA <= distDI && neqv(E, A) && neqv(B, A) {
			out.w += 16.0
		}
		if steepDirectionThreshold*distDI <= distHA && neqv(E, I) && neqv(F, I) {
			out.w += 64.0
		}
	}

	if br.y == blendDominant || (br.y == blendNormal &&
		!((br.x != blendNone && !isPixEqual(E, I)) || (br.z != blendNone && !isPixEqual(E, A)) ||
			(isPixEqual(I, F) && isPixEqual(F, C) && isPixEqual(C, B) && isPixEqual(B, A) && !isPixEqual(E, C)))) {
		out.y += 4.0
		distBI := distYCbCr(B, I)
		distFA := distYCbCr(F, A)
		if steepDirectionThreshold*distBI <= distFA && neqv(E, I) && neqv(H, I) {
			out.y += 16.0
		}
		if steepDirectionThreshold*distFA <= distBI && neqv(E, A) && neqv(D, A) {
			out.y += 64.0
		}
	}

	if br.x == blendDominant || (br.x == blendNormal &&
		!((br.w != blendNone && !isPixEqual(E, C)) || (br.y != blendNone && !isPixEqual(E, G)) ||
			(isPixEqual(C, B) && isPixEqual(B, A) && isPixEqual(A, D) && isPixEqual(D, G) && !isPixEqual(E, A)))) {
		out.x += 4.0
		distDC := distYCbCr(D, C)
		distBG := distYCbCr(B, G)
		if steepDirectionThreshold*distDC <= distBG && neqv(E, C) && neqv(F, C) {
			out.x += 16.0
		}
		if steepDirectionThreshold*distBG <= distDC && neqv(E, G) && neqv(H, G) {
			out.x += 64.0
		}
	}

	return out / 255.0
}
`

const shaderXBRZ1 = `
//kage:unit pixels
package main

const blendNone     float = 0.0
const luminanceWeight     float = 1.0
const equalColorTolerance float = 30.0 / 255.0

const inputW float = 240.0
const inputH float = 160.0

// Output upscale factor — must match xBRZScale in filters.go.
const scale float = 4.0

var OutputSize vec2

func distYCbCr(pixA, pixB vec3) float {
	w := vec3(0.2627, 0.6780, 0.0593)
	scaleB := 0.5 / (1.0 - w.b)
	scaleR := 0.5 / (1.0 - w.r)
	diff := pixA - pixB
	Y := dot(diff, w)
	Cb := scaleB * (diff.b - Y)
	Cr := scaleR * (diff.r - Y)
	return sqrt((luminanceWeight*Y)*(luminanceWeight*Y) + Cb*Cb + Cr*Cr)
}

// getLeftRatio — anti-aliased line ratio for diagonal blend
// reconstruction. ⇄ upstream get_left_ratio.
func getLeftRatio(center, origin, direction, scaleXY vec2) float {
	p0 := center - origin
	proj := direction * (dot(p0, direction) / dot(direction, direction))
	distv := p0 - proj
	orth := vec2(-direction.y, direction.x)
	side := sign(dot(p0, orth))
	v := side * length(distv*scaleXY)
	return smoothstep(-sqrt(2.0)/2.0, sqrt(2.0)/2.0, v)
}

func Fragment(_ vec4, srcPos vec2, _ vec4) vec4 {
	// Ebiten stores images inside a texture atlas, so srcPos is in
	// atlas coords offset by imageSrc0Origin(). Derive native-pixel
	// space from the offset-into-source position, divided by the 4x
	// pre-scale. pos = sub-pixel fract centered on 0.
	src0Orig := imageSrc0Origin()
	srcLocal := srcPos - src0Orig          // [0, 960) x [0, 640)
	srcNative := srcLocal / scale          // [0, 240) x [0, 160)
	pos := fract(srcNative) - vec2(0.5)
	coord := srcNative - pos               // integer center, native space

	// Sample neighborhood in srcScaled. Each native pixel N maps to a
	// 4x4 block in srcScaled centered at originSrc0 + N*scale.
	B := imageSrc0At(src0Orig + (coord+vec2(0, -1))*scale).rgb
	D := imageSrc0At(src0Orig + (coord+vec2(-1, 0))*scale).rgb
	E := imageSrc0At(src0Orig + (coord+vec2(0, 0))*scale).rgb
	F := imageSrc0At(src0Orig + (coord+vec2(1, 0))*scale).rgb
	H := imageSrc0At(src0Orig + (coord+vec2(0, 1))*scale).rgb

	// info-map: bound as image 1. Ebiten requires all bound images
	// in one DrawRectShader to be the same size, so they share the
	// same origin as imageSrc0 — there's no imageSrc1Origin().
	info := floor(imageSrc1At(src0Orig+coord*scale)*255.0 + vec4(0.5))

	blend := floor(mod(info, 4.0))
	doLine := floor(mod(info/4.0, 4.0))
	shallow := floor(mod(info/16.0, 4.0))
	steep := floor(mod(info/64.0, 4.0))

	res := E
	scaleXY := vec2(scale, scale)

	// Corner z (bottom-right): blend toward H or F.
	if blend.z > blendNone {
		origin := vec2(0.0, 1.0/sqrt(2.0))
		direction := vec2(1.0, -1.0)
		if doLine.z > 0.0 {
			if shallow.z > 0.0 {
				origin = vec2(0.0, 0.25)
			} else {
				origin = vec2(0.0, 0.5)
			}
			direction.x += shallow.z
			direction.y -= steep.z
		}
		blendPix := mix(H, F, step(distYCbCr(E, F), distYCbCr(E, H)))
		res = mix(res, blendPix, getLeftRatio(pos, origin, direction, scaleXY))
	}

	// Corner w (bottom-left): blend toward H or D.
	if blend.w > blendNone {
		origin := vec2(-1.0/sqrt(2.0), 0.0)
		direction := vec2(1.0, 1.0)
		if doLine.w > 0.0 {
			if shallow.w > 0.0 {
				origin = vec2(-0.25, 0.0)
			} else {
				origin = vec2(-0.5, 0.0)
			}
			direction.y += shallow.w
			direction.x += steep.w
		}
		blendPix := mix(H, D, step(distYCbCr(E, D), distYCbCr(E, H)))
		res = mix(res, blendPix, getLeftRatio(pos, origin, direction, scaleXY))
	}

	// Corner y (top-right): blend toward F or B.
	if blend.y > blendNone {
		origin := vec2(1.0/sqrt(2.0), 0.0)
		direction := vec2(-1.0, -1.0)
		if doLine.y > 0.0 {
			if shallow.y > 0.0 {
				origin = vec2(0.25, 0.0)
			} else {
				origin = vec2(0.5, 0.0)
			}
			direction.y -= shallow.y
			direction.x -= steep.y
		}
		blendPix := mix(F, B, step(distYCbCr(E, B), distYCbCr(E, F)))
		res = mix(res, blendPix, getLeftRatio(pos, origin, direction, scaleXY))
	}

	// Corner x (top-left): blend toward D or B.
	if blend.x > blendNone {
		origin := vec2(0.0, -1.0/sqrt(2.0))
		direction := vec2(-1.0, 1.0)
		if doLine.x > 0.0 {
			if shallow.x > 0.0 {
				origin = vec2(0.0, -0.25)
			} else {
				origin = vec2(0.0, -0.5)
			}
			direction.x -= shallow.x
			direction.y += steep.x
		}
		blendPix := mix(D, B, step(distYCbCr(E, B), distYCbCr(E, D)))
		res = mix(res, blendPix, getLeftRatio(pos, origin, direction, scaleXY))
	}

	return vec4(res, 1.0)
}
`

