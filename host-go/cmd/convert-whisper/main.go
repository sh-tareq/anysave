// Converts a Hugging Face Whisper model (model.safetensors, float32) into whisper.cpp's ggml
// format (float16), the way whisper.cpp's convert-h5-to-ggml.py does, without PyTorch.
//
// The header (model sizes, mel filters, vocabulary) is copied from an existing ggml model of the
// same architecture, after checking the sizes match; only the weights come from the new model.
//
// usage: conv <base-ggml.bin> <config.json> <model.safetensors> <out.bin>
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

var convMap = map[string]string{
	"self_attn.k_proj": "attn.key", "self_attn.q_proj": "attn.query", "self_attn.v_proj": "attn.value",
	"self_attn.out_proj": "attn.out", "self_attn_layer_norm": "attn_ln",
	"encoder_attn.q_proj": "cross_attn.query", "encoder_attn.v_proj": "cross_attn.value",
	"encoder_attn.out_proj": "cross_attn.out", "encoder_attn_layer_norm": "cross_attn_ln",
	"fc1": "mlp.0", "fc2": "mlp.2", "final_layer_norm": "mlp_ln",
	"encoder.layer_norm.bias": "encoder.ln_post.bias", "encoder.layer_norm.weight": "encoder.ln_post.weight",
	"encoder.embed_positions.weight": "encoder.positional_embedding",
	"decoder.layer_norm.bias":        "decoder.ln.bias", "decoder.layer_norm.weight": "decoder.ln.weight",
	"decoder.embed_positions.weight": "decoder.positional_embedding",
	"decoder.embed_tokens.weight":    "decoder.token_embedding.weight",
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rd32(r io.Reader) int32 {
	var v int32
	must(binary.Read(r, binary.LittleEndian, &v))
	return v
}

// header reads the base model up to the end of its vocabulary and returns those bytes.
func header(path string) ([]byte, []int32) {
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	r := bufio.NewReader(f)
	var buf []byte
	take := func(n int) []byte {
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		must(err)
		buf = append(buf, b...)
		return b
	}
	i32 := func() int32 { return int32(binary.LittleEndian.Uint32(take(4))) }
	if i32() != 0x67676d6c {
		must(fmt.Errorf("%s is not a ggml model", path))
	}
	hp := make([]int32, 11)
	for i := range hp {
		hp[i] = i32()
	}
	nMel, nFFT := i32(), i32()
	take(int(nMel) * int(nFFT) * 4)
	nVocab := i32()
	for i := int32(0); i < nVocab; i++ {
		take(int(i32()))
	}
	return buf, hp
}

// f32 -> f16 with round to nearest even.
func f16(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int((b>>23)&0xff) - 127 + 15
	man := b & 0x7fffff
	switch {
	case (b>>23)&0xff == 0xff: // inf / nan
		if man != 0 {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	case exp >= 0x1f:
		return sign | 0x7c00
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		man |= 0x800000
		shift := uint(14 - exp)
		half := uint32(1) << (shift - 1)
		r := man >> shift
		if rem := man & ((1 << shift) - 1); rem > half || (rem == half && r&1 == 1) {
			r++
		}
		return sign | uint16(r)
	}
	r := uint16(exp)<<10 | uint16(man>>13)
	if rem := man & 0x1fff; rem > 0x1000 || (rem == 0x1000 && r&1 == 1) {
		r++
	}
	return sign | r
}

type stInfo struct {
	Dtype   string   `json:"dtype"`
	Shape   []int64  `json:"shape"`
	Offsets [2]int64 `json:"data_offsets"`
}

func mapName(name string) (string, bool) {
	if name == "proj_out.weight" {
		return "", false
	}
	parts := strings.Split(name, ".")[1:] // drop "model."
	if len(parts) > 2 && parts[1] == "layers" {
		parts[1] = "blocks"
		mid := strings.Join(parts[3:len(parts)-1], ".")
		var mapped string
		if mid == "encoder_attn.k_proj" {
			if parts[0] == "encoder" {
				mapped = "attn.key"
			} else {
				mapped = "cross_attn.key"
			}
		} else if m, ok := convMap[mid]; ok {
			mapped = m
		} else {
			must(fmt.Errorf("unknown layer part %q in %s", mid, name))
		}
		return strings.Join(append(append(parts[:3:3], mapped), parts[len(parts)-1]), "."), true
	}
	n := strings.Join(parts, ".")
	if m, ok := convMap[n]; ok {
		n = m
	}
	return n, true
}

func main() {
	if len(os.Args) != 5 {
		fmt.Println("usage: conv <base-ggml.bin> <config.json> <model.safetensors> <out.bin>")
		os.Exit(2)
	}
	head, hp := header(os.Args[1])
	var cfg map[string]any
	b, err := os.ReadFile(os.Args[2])
	must(err)
	must(json.Unmarshal(b, &cfg))
	num := func(k string) int32 { v, _ := cfg[k].(float64); return int32(v) }
	want := []int32{num("vocab_size"), num("max_source_positions"), num("d_model"), num("encoder_attention_heads"),
		num("encoder_layers"), num("max_target_positions"), num("d_model"), num("decoder_attention_heads"),
		num("decoder_layers"), num("num_mel_bins")}
	for i, w := range want {
		if hp[i] != w {
			must(fmt.Errorf("base model size %d is %d, new model has %d: not the same architecture", i, hp[i], w))
		}
	}
	binary.LittleEndian.PutUint32(head[4+10*4:], 1) // ftype: f16

	st, err := os.Open(os.Args[3])
	must(err)
	defer st.Close()
	var hlen uint64
	must(binary.Read(st, binary.LittleEndian, &hlen))
	hb := make([]byte, hlen)
	_, err = io.ReadFull(st, hb)
	must(err)
	var raw map[string]json.RawMessage
	must(json.Unmarshal(hb, &raw))
	dataStart := int64(8 + hlen)

	out, err := os.Create(os.Args[4])
	must(err)
	w := bufio.NewWriterSize(out, 8<<20)
	_, err = w.Write(head)
	must(err)

	names := make([]string, 0, len(raw))
	for k := range raw {
		if k != "__metadata__" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	count := 0
	for _, src := range names {
		var t stInfo
		must(json.Unmarshal(raw[src], &t))
		name, ok := mapName(src)
		if !ok {
			continue
		}
		if t.Dtype != "F32" {
			must(fmt.Errorf("%s is %s; only F32 is supported", src, t.Dtype))
		}
		var shape []int64
		for _, d := range t.Shape {
			if d != 1 {
				shape = append(shape, d) // squeeze
			}
		}
		if name == "encoder.conv1.bias" || name == "encoder.conv2.bias" {
			shape = []int64{shape[0], 1}
		}
		ftype := int32(1)
		if len(shape) < 2 || name == "encoder.conv1.bias" || name == "encoder.conv2.bias" ||
			name == "encoder.positional_embedding" || name == "decoder.positional_embedding" {
			ftype = 0
		}
		n := (t.Offsets[1] - t.Offsets[0]) / 4
		data := make([]byte, t.Offsets[1]-t.Offsets[0])
		_, err := st.ReadAt(data, dataStart+t.Offsets[0])
		must(err)
		nb := []byte(name)
		for _, v := range []int32{int32(len(shape)), int32(len(nb)), ftype} {
			must(binary.Write(w, binary.LittleEndian, v))
		}
		for i := len(shape) - 1; i >= 0; i-- {
			must(binary.Write(w, binary.LittleEndian, int32(shape[i])))
		}
		_, err = w.Write(nb)
		must(err)
		if ftype == 0 {
			_, err = w.Write(data)
			must(err)
		} else {
			o := make([]byte, n*2)
			for i := int64(0); i < n; i++ {
				binary.LittleEndian.PutUint16(o[i*2:], f16(math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))))
			}
			_, err = w.Write(o)
			must(err)
		}
		count++
	}
	must(w.Flush())
	must(out.Close())
	fmt.Printf("wrote %d tensors to %s\n", count, os.Args[4])
}
