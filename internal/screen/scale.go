package screen

import "image"

// Scaler achica cuadros RGBA con un filtro de área: cada píxel de salida es el promedio de los
// de entrada que cubre, pesado por cuánto de cada uno cubre (como INTER_AREA de OpenCV o "Fant"
// de WIC). Para achicar es lo que mejor conserva el texto: sin los halos de bicúbico/Lanczos ni el
// aliasing de bilineal. Es separable (primero horizontal, después vertical) con pesos enteros
// precalculados por tamaño; los pesos y el buffer intermedio se reusan entre llamadas. No agranda.
// No es seguro para goroutines concurrentes.
type Scaler struct {
	x, y axisWeights
	tmp  []uint32 // pasada horizontal: ancho de salida × alto de entrada × 4, en 16.16
	acc  []uint64 // una fila de la pasada vertical
}

// axisWeights son los pesos de un eje de src píxeles a dst: la salida j toma count[j] entradas
// desde first[j], con pesos en weights (suman exactamente 1<<16 por salida).
type axisWeights struct {
	src, dst int
	first    []int
	count    []int
	offset   []int // dónde empiezan en weights los pesos de cada salida
	weights  []uint32
}

const weightOne = 1 << 16

func (w *axisWeights) build(src, dst int) {
	if w.src == src && w.dst == dst {
		return
	}
	w.src, w.dst = src, dst
	w.first, w.count, w.offset, w.weights = make([]int, dst), make([]int, dst), make([]int, dst), w.weights[:0]
	// En unidades de 1/dst de píxel de entrada: la salida j cubre [j·src, (j+1)·src) y la entrada
	// i ocupa [i·dst, (i+1)·dst).
	for j := range dst {
		start, end := j*src, (j+1)*src
		first, last := start/dst, (end-1)/dst
		w.first[j], w.count[j], w.offset[j] = first, last-first+1, len(w.weights)
		sum, biggest := uint32(0), len(w.weights)
		for i := first; i <= last; i++ {
			overlap := min(end, (i+1)*dst) - max(start, i*dst)
			weight := uint32(uint64(overlap) * weightOne / uint64(src))
			w.weights = append(w.weights, weight)
			sum += weight
			if weight > w.weights[biggest] {
				biggest = len(w.weights) - 1
			}
		}
		w.weights[biggest] += weightOne - sum // lo que perdió el redondeo, al peso más grande
	}
}

// FitSize es el tamaño que entra en maxW×maxH conservando la proporción, sin agrandar (mínimo 1).
func FitSize(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}
	if w*maxH > h*maxW { // manda el ancho
		return maxW, max(1, (h*maxW+w/2)/w)
	}
	return max(1, (w*maxH+h/2)/h), maxH
}

// Scale achica src al tamaño de dst (no más grande que src en ningún eje). Con el mismo tamaño
// copia.
func (s *Scaler) Scale(dst, src *image.RGBA) { s.ScaleRect(dst, src, src.Rect) }

// ScaleRect es Scale pero solo recalcula los píxeles de dst que dependen de r (en coordenadas de
// src): el visor actualiza así solo los tiles que llegaron, con el mismo resultado que Scale en esa
// zona (TestScaleRectMatchesScale).
func (s *Scaler) ScaleRect(dst, src *image.RGBA, r image.Rectangle) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := min(dst.Rect.Dx(), sw), min(dst.Rect.Dy(), sh)
	r = r.Sub(src.Rect.Min).Intersect(image.Rect(0, 0, sw, sh))
	if r.Empty() || dw <= 0 || dh <= 0 {
		return
	}
	if dw == sw && dh == sh {
		width := r.Dx() * 4
		for y := r.Min.Y; y < r.Max.Y; y++ {
			so := src.PixOffset(src.Rect.Min.X+r.Min.X, src.Rect.Min.Y+y)
			do := dst.PixOffset(dst.Rect.Min.X+r.Min.X, dst.Rect.Min.Y+y)
			copy(dst.Pix[do:do+width], src.Pix[so:so+width])
		}
		return
	}
	s.x.build(sw, dw)
	s.y.build(sh, dh)
	// La salida j cubre la entrada [j·sw/dw, (j+1)·sw/dw): toca a r si j está en [j0, j1).
	j0, j1 := r.Min.X*dw/sw, min(dw, (r.Max.X*dw+sw-1)/sw)
	i0, i1 := r.Min.Y*dh/sh, min(dh, (r.Max.Y*dh+sh-1)/sh)
	// Filas de entrada que usan esas salidas (la pasada vertical las necesita todas).
	y0, y1 := s.y.first[i0], s.y.first[i1-1]+s.y.count[i1-1]
	cols := (j1 - j0) * 4
	if need := (y1 - y0) * cols; cap(s.tmp) < need {
		s.tmp = make([]uint32, need)
	} else {
		s.tmp = s.tmp[:need]
	}
	// Horizontal: cada fila de entrada → las columnas de salida j0..j1 en 16.16 (255·2^16 entra en
	// uint32).
	for y := y0; y < y1; y++ {
		row := src.Pix[src.PixOffset(src.Rect.Min.X, src.Rect.Min.Y+y):]
		out := s.tmp[(y-y0)*cols:]
		for j := j0; j < j1; j++ {
			var r, g, b, a uint32
			weights := s.x.weights[s.x.offset[j]:][:s.x.count[j]]
			p := s.x.first[j] * 4
			for _, weight := range weights {
				r += weight * uint32(row[p])
				g += weight * uint32(row[p+1])
				b += weight * uint32(row[p+2])
				a += weight * uint32(row[p+3])
				p += 4
			}
			o := (j - j0) * 4
			out[o], out[o+1], out[o+2], out[o+3] = r, g, b, a
		}
	}
	// Vertical: 16.16 × 16 bits de peso = 32 bits de fracción, en uint64; redondeo al más cercano.
	// Fila por fila de entrada, así se recorre tmp en orden.
	if cap(s.acc) < cols {
		s.acc = make([]uint64, cols)
	}
	acc := s.acc[:cols]
	for i := i0; i < i1; i++ {
		clear(acc)
		for k, weight := range s.y.weights[s.y.offset[i]:][:s.y.count[i]] {
			row := s.tmp[(s.y.first[i]+k-y0)*cols:][:cols]
			for j, value := range row {
				acc[j] += uint64(weight) * uint64(value)
			}
		}
		out := dst.Pix[dst.PixOffset(dst.Rect.Min.X+j0, dst.Rect.Min.Y+i):][:cols]
		for j, value := range acc {
			out[j] = uint8((value + 1<<31) >> 32)
		}
	}
}
