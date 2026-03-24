package fireworks

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var colors = [][]lipgloss.Color{
	{"196", "203", "210", "217"}, // red gradient
	{"220", "221", "222", "229"}, // gold gradient
	{"42", "48", "84", "120"},    // green gradient
	{"44", "45", "51", "87"},     // cyan gradient
	{"204", "211", "218", "225"}, // pink gradient
	{"99", "105", "141", "177"},  // purple gradient
	{"208", "214", "215", "222"}, // orange gradient
}

// Dense character set — alphanumeric + symbols.
var chars = []byte("@#&$%XYZ0123456789*+=^~:;.,!?abcdefghijklmnopqrstuvwxyz")

type cell struct {
	char  byte
	color lipgloss.Color
	life  float64 // 0..1 brightness
}

type shell struct {
	launchX    float64 // x position (fraction of width)
	burstY     float64 // y position (fraction of height from top)
	burstFrame int     // frame when burst happens
	colorGroup int
	seed       int64
}

const (
	particleCount = 80 // particles per burst
	trailLen      = 3
)

// 3 shells, fired sequentially with gaps.
var shells = []shell{
	{launchX: 0.50, burstY: 0.22, burstFrame: 0, colorGroup: 0, seed: 42},
	{launchX: 0.25, burstY: 0.28, burstFrame: 30, colorGroup: 1, seed: 99},
	{launchX: 0.75, burstY: 0.18, burstFrame: 55, colorGroup: 2, seed: 37},
}

// Total show duration: last shell burst + launch time + particle lifetime + fade.
const showDuration = 55 + 8 + 50

// Render returns a fireworks animation frame.
// frame is the raw tick counter from the spinner (~120ms per tick).
// Returns the done message once the show is over.
func Render(frame, width, height int) string {
	if width < 20 || height < 5 {
		return ""
	}

	if frame > showDuration {
		return renderDoneMessage(width, height)
	}

	grid := make([][]cell, height)
	for r := range grid {
		grid[r] = make([]cell, width)
	}

	gravity := 0.03 // tuned for ~8 FPS tick rate

	for _, sh := range shells {
		age := frame - sh.burstFrame
		if age < 0 {
			continue // hasn't launched yet
		}

		cx := int(sh.launchX * float64(width))
		cy := int(sh.burstY * float64(height))
		cg := sh.colorGroup % len(colors)

		// Launch trail (frames 0-7).
		if age < 8 {
			progress := float64(age) / 7.0
			trailY := height - 1 - int(progress*float64(height-cy))
			for dy := 0; dy < 4 && trailY+dy < height; dy++ {
				row := trailY + dy
				if row >= 0 && row < height && cx >= 0 && cx < width {
					brightness := 1.0 - float64(dy)*0.25
					if brightness > grid[row][cx].life {
						grid[row][cx] = cell{char: '|', color: colors[cg][0], life: brightness}
					}
				}
			}
			continue
		}

		// Burst phase.
		burstAge := age - 8
		rng := rand.New(rand.NewSource(sh.seed))

		for i := range particleCount {
			angle := rng.Float64() * 2 * math.Pi
			speed := 0.3 + rng.Float64()*1.2
			ch := chars[rng.Intn(len(chars))]
			maxLife := 25 + rng.Intn(25) // 25-50 frames at tick rate

			if burstAge > maxLife {
				continue
			}

			vx := math.Cos(angle) * speed * 2 // stretch for terminal aspect
			vy := math.Sin(angle)*speed - 0.3 // slight upward bias

			t := float64(burstAge)
			px := float64(cx) + vx*t
			py := float64(cy) + vy*t + 0.5*gravity*t*t

			col := int(math.Round(px))
			row := int(math.Round(py))

			if row < 0 || row >= height || col < 0 || col >= width {
				continue
			}

			lifeFrac := 1.0 - float64(burstAge)/float64(maxLife)
			if lifeFrac <= 0 {
				continue
			}

			colorIdx := int((1 - lifeFrac) * float64(len(colors[cg])-1))
			if colorIdx >= len(colors[cg]) {
				colorIdx = len(colors[cg]) - 1
			}

			if lifeFrac < 0.3 {
				ch = ".·:;"[i%4]
			} else if lifeFrac < 0.6 {
				ch = "*+=-~"[i%5]
			}

			if lifeFrac > grid[row][col].life {
				grid[row][col] = cell{char: ch, color: colors[cg][colorIdx], life: lifeFrac}
			}

			// Trails.
			for tr := 1; tr <= trailLen; tr++ {
				tt := float64(burstAge - tr)
				if tt < 0 {
					break
				}
				tpx := float64(cx) + vx*tt
				tpy := float64(cy) + vy*tt + 0.5*gravity*tt*tt
				tc := int(math.Round(tpx))
				trow := int(math.Round(tpy))
				if trow >= 0 && trow < height && tc >= 0 && tc < width {
					trailLife := lifeFrac * (1.0 - float64(tr)*0.3)
					if trailLife > 0 && trailLife > grid[trow][tc].life {
						trailColorIdx := len(colors[cg]) - 1
						grid[trow][tc] = cell{char: '.', color: colors[cg][trailColorIdx], life: trailLife}
					}
				}
			}
		}
	}

	// Render grid to string.
	msgRow := height / 2
	center := lipgloss.NewStyle().Align(lipgloss.Center).Width(width)
	greenBold := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	dimText := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

	var sb strings.Builder
	for r := range height {
		switch r {
		case msgRow:
			sb.WriteString(center.Render(greenBold.Render("All clear!")))
		case msgRow + 1:
			sb.WriteString(center.Render(dimText.Render("No PRs need your review right now.")))
		default:
			sb.WriteString(renderGridRow(grid[r]))
		}
		if r < height-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func renderDoneMessage(width, height int) string {
	center := lipgloss.NewStyle().Align(lipgloss.Center).Width(width)
	greenBold := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	dimText := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

	msgRow := height / 2
	var sb strings.Builder
	for r := range height {
		switch r {
		case msgRow:
			sb.WriteString(center.Render(greenBold.Render("All clear!")))
		case msgRow + 1:
			sb.WriteString(center.Render(dimText.Render("No PRs need your review right now.")))
		}
		if r < height-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func renderGridRow(row []cell) string {
	last := -1
	for i := len(row) - 1; i >= 0; i-- {
		if row[i].char != 0 {
			last = i
			break
		}
	}
	if last < 0 {
		return ""
	}

	type segment struct {
		col    int
		styled string
	}
	var segs []segment
	for i := 0; i <= last; i++ {
		c := row[i]
		if c.char == 0 {
			continue
		}
		var s lipgloss.Style
		if c.life > 0.7 {
			s = lipgloss.NewStyle().Foreground(c.color).Bold(true)
		} else {
			s = lipgloss.NewStyle().Foreground(c.color)
		}
		segs = append(segs, segment{col: i, styled: s.Render(string(c.char))})
	}

	sort.Slice(segs, func(i, j int) bool { return segs[i].col < segs[j].col })

	var sb strings.Builder
	cursor := 0
	for _, seg := range segs {
		if seg.col > cursor {
			sb.WriteString(strings.Repeat(" ", seg.col-cursor))
		}
		sb.WriteString(seg.styled)
		cursor = seg.col + 1
	}
	return sb.String()
}
