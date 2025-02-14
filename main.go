package main

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	logging "github.com/tsujio/game-logging-server/client"
	"github.com/tsujio/game-rhinoceros/effectutil"
	"github.com/tsujio/game-rhinoceros/loggingutil"
	resourceutilv2 "github.com/tsujio/game-rhinoceros/resourceutil"
	"github.com/tsujio/game-rhinoceros/touchutil"
	"github.com/tsujio/game-util/mathutil"
	"github.com/tsujio/game-util/resourceutil"
)

const (
	gameName       = "rhinoceros"
	screenWidth    = 640
	screenHeight   = 480
	rhinoX         = 170
	rhinoY         = 370
	rhinoR         = 10
	rhinoSpeed     = 2.5
	rhinoRushSpeed = 6
	enemyR         = 8
	gageMax        = 300
	cameraY        = -100
	screenZ        = 50
)

var rhinoZ = mathutil.ConvertCoordinateScreenToWorld(
	mathutil.NewVector2D(rhinoX, rhinoY),
	mathutil.NewVector3D(math.NaN(), 0, math.NaN()),
	cameraY,
	screenZ,
	screenWidth,
	screenHeight,
).Z

var (
	emptyImage = func() *ebiten.Image {
		img := ebiten.NewImage(3, 3)
		img.Fill(color.White)
		return img
	}()
	emptySubImage = emptyImage.SubImage(image.Rect(1, 1, 2, 2)).(*ebiten.Image)
)

//go:embed resources/secret resources/*.png resources/*.ttf resources/*.dat
var resources embed.FS

var imgLoader = resourceutilv2.NewImageLoader(resources, "resources/rhinoceros.png")

var (
	fontFaceL, fontFaceM, fontFaceS = resourceutilv2.ForceLoadFont(resources, "resources/PressStart2P-Regular.ttf")
	audioContext                    = audio.NewContext(48000)
	gameStartAudioData              = resourceutil.ForceLoadDecodedAudio(resources, "resources/魔王魂 効果音 システム49.mp3.dat", audioContext)
	gameOverAudioData               = resourceutil.ForceLoadDecodedAudio(resources, "resources/魔王魂 効果音 システム32.mp3.dat", audioContext)
	hitAudioData                    = resourceutil.ForceLoadDecodedAudio(resources, "resources/maou_se_battle12.mp3.dat", audioContext)
	runAudioData                    = resourceutil.ForceLoadDecodedAudio(resources, "resources/maou_se_sound_ignition01.mp3.dat", audioContext)
	chargeAudioData                 = resourceutil.ForceLoadDecodedAudio(resources, "resources/maou_se_sound17.mp3.dat", audioContext)
	rhinoImg                        = imgLoader.ExtractList(0, 0, 110, 60, 1, 4)
	rhinoHitImg                     = imgLoader.Extract(0, 240, 110, 70)
	enemyImg                        = imgLoader.ExtractList(150, 0, 120, 70, 1, 2)
	treeImg                         = imgLoader.Extract(290, 0, 150, 120)
	weedImg                         = imgLoader.Extract(290, 130, 70, 40)
	//cloudImgS                       = imgLoader.Extract(290, 190, 80, 40)
	cloudImgL     = imgLoader.Extract(290, 240, 130, 60)
	backgroundImg = imgLoader.Extract(560, 0, 60, 480)
)

type Enemy struct {
	ticks        uint64
	pos, prevPos *mathutil.Vector2D
	hitV         *mathutil.Vector2D
	hit          bool
	runner       *GameRunner
}

func (e *Enemy) update() {
	e.ticks++

	if !e.hit {
		e.prevPos = e.pos

		speed := 1.5
		if e.runner.rush {
			speed += rhinoRushSpeed
		} else {
			speed += rhinoSpeed
		}
		e.pos = e.pos.Add(mathutil.NewVector2D(-speed, 0))
	} else {
		e.pos = e.pos.Add(e.hitV)
		e.hitV = e.hitV.Add(mathutil.NewVector2D(0, 1))
	}
}

func (e *Enemy) draw(dst *ebiten.Image) {
	index := int(e.ticks / 10 % 2)
	img := enemyImg[index]
	size := img.Bounds().Size()
	opts := &ebiten.DrawImageOptions{}
	opts.GeoM.Translate(-float64(size.X)/2, -float64(size.Y))
	opts.GeoM.Scale(0.7, 0.7)
	if e.hit {
		opts.GeoM.Rotate(2 * math.Pi / 30.0 * float64(e.ticks%30))
	}
	opts.GeoM.Translate(e.pos.X, e.pos.Y-float64(index*5))
	dst.DrawImage(img, opts)
}

type BackgroundObject struct {
	typ    string
	pos    *mathutil.Vector3D
	runner *GameRunner
}

func (o *BackgroundObject) update() {
	var speed float64
	if o.runner.rush {
		speed = rhinoRushSpeed
	} else {
		speed = rhinoSpeed
	}
	v := mathutil.NewVector3D(-speed, 0, 0)
	o.pos = o.pos.Add(v)
}

func (o *BackgroundObject) draw(dst *ebiten.Image) {
	pos := mathutil.ConvertCoordinateWorldToScreen(o.pos, cameraY, screenZ, screenWidth, screenHeight)

	var img *ebiten.Image
	scale := 1.0
	switch o.typ {
	case "tree":
		img = treeImg
	case "weed":
		img = weedImg
		scale = 0.4
	case "cloud":
		img = cloudImgL
		scale = 200.0
	}

	size := img.Bounds().Size()
	opts := &ebiten.DrawImageOptions{}
	opts.GeoM.Translate(-float64(size.X)/2, -float64(size.Y))
	scale *= screenZ / o.pos.Z
	opts.GeoM.Scale(scale, scale)
	opts.GeoM.Translate(pos.X, pos.Y)
	if o.pos.Z < rhinoZ {
		opts.ColorScale.ScaleAlpha(0.7)
	}
	dst.DrawImage(img, opts)
}

type GameRunner struct {
	ticks          uint64
	game           *Game
	mute           bool
	gameOver       bool
	gage           int
	rush           bool
	hitCountInRush int
	ticksAtHit     uint64
	enemies        []Enemy
	objects        []BackgroundObject
	effects        []effectutil.Effect
	score          int
}

func (r *GameRunner) createBackgroundObject(typ string, screenX float64) *BackgroundObject {
	var y, zOffset, zRange float64
	switch typ {
	case "tree", "weed":
		y = 0
		zOffset = 20
		zRange = 700
	case "cloud":
		y = -50000
		zOffset = 9999
		zRange = 99999
	}
	wPos := mathutil.NewVector3D(0, y, zOffset+r.game.random.Float64()*zRange)
	sPos := mathutil.ConvertCoordinateWorldToScreen(wPos, cameraY, screenZ, screenWidth, screenHeight)
	sPos.X = screenX
	pos := mathutil.ConvertCoordinateScreenToWorld(
		sPos,
		mathutil.NewVector3D(math.NaN(), math.NaN(), wPos.Z),
		cameraY,
		screenZ,
		screenWidth,
		screenHeight,
	)
	return &BackgroundObject{
		typ:    typ,
		pos:    pos,
		runner: r,
	}
}

func (r *GameRunner) update(touches []touchutil.Touch) {
	if r.gameOver {
		return
	}

	r.ticks++

	if !r.rush {
		if touchutil.AnyTouchesJustTouched(touches) ||
			(r.gage > 0 && touchutil.AnyTouchesActive(touches)) {
			if !r.mute && r.gage == 0 {
				audioContext.NewPlayerFromBytes(chargeAudioData).Play()
			}

			r.gage += 1
			if r.gage > gageMax {
				r.gage = gageMax
			}
		}

		if r.gage > 0 && touchutil.AllTouchesJustReleased(touches) {
			r.rush = true
		}

		if !r.mute && r.ticks%20 == 0 {
			p := audioContext.NewPlayerFromBytes(runAudioData)
			p.SetVolume(0.1)
			p.Play()
		}
	} else {
		r.gage -= 1

		if r.gage > 0 && r.gage%10 == 0 {
			r.effects = append(r.effects, effectutil.NewSplashEffect(
				rhinoX-20,
				rhinoY,
				15,
				&effectutil.SplashEffectOptions{
					Count:           3,
					Color:           color.White,
					Size:            10,
					AngularVelocity: math.Pi / 20,
					AngleMin:        math.Pi,
					AngleMax:        math.Pi * 4 / 3,
					Speed:           8,
					Ay:              0.3,
					Random:          r.game.random,
				},
			))
		}

		if r.gage <= 0 {
			r.rush = false
			r.gage = 0
			r.hitCountInRush = 0
		}

		if !r.mute && r.ticks%10 == 0 {
			p := audioContext.NewPlayerFromBytes(runAudioData)
			p.SetVolume(0.1)
			p.Play()
		}
	}

	if r.ticks > 120 && r.game.random.Int()%60 == 0 {
		pos := mathutil.NewVector2D(screenWidth+50, rhinoY)
		e := Enemy{
			pos:     pos,
			prevPos: pos,
			runner:  r,
		}
		r.enemies = append(r.enemies, e)
	}

	if r.game.random.Int()%60 == 0 {
		o := r.createBackgroundObject("tree", screenWidth+100)
		r.objects = append(r.objects, *o)
	}

	if true {
		o := r.createBackgroundObject("weed", screenWidth+100)
		r.objects = append(r.objects, *o)
	}

	if r.game.random.Int()%60 == 0 {
		o := r.createBackgroundObject("cloud", screenWidth+100)
		r.objects = append(r.objects, *o)
	}

	for i := range r.enemies {
		e := &r.enemies[i]

		if !e.hit && mathutil.CapsulesCollide(
			mathutil.NewVector2D(rhinoX, rhinoY),
			mathutil.NewVector2D(0, 0),
			rhinoR,
			e.prevPos,
			e.pos.Sub(e.prevPos),
			enemyR,
		) {
			if r.rush {
				e.hitV = mathutil.NewVector2D(
					5+10*r.game.random.Float64(),
					-(10 + 20*r.game.random.Float64()),
				)
				e.hit = true
				r.ticksAtHit = r.ticks
				r.hitCountInRush++
				gain := 10 * r.hitCountInRush
				r.score += gain

				r.effects = append(r.effects, effectutil.NewGainEffect(
					rhinoX+50+(r.game.random.Float64()-0.5)*100,
					rhinoY-30+(r.game.random.Float64()-0.5)*100,
					60,
					&effectutil.GainEffectOptions{
						Gain: gain,
						Face: fontFaceM,
					},
				))

				r.effects = append(r.effects, effectutil.NewSplashEffect(
					rhinoX+50,
					rhinoY-30,
					999,
					&effectutil.SplashEffectOptions{
						Count:           5,
						Color:           color.RGBA{0xff, 0xff, 0, 0xff},
						Size:            10,
						AngularVelocity: math.Pi / 20,
						AngleMin:        -math.Pi / 2,
						AngleMax:        math.Pi / 4,
						Speed:           10,
						Ay:              0.2,
						Random:          r.game.random,
					},
				))

				if !r.mute {
					audioContext.NewPlayerFromBytes(hitAudioData).Play()
				}
			} else {
				r.gameOver = true
			}
		}

		e.update()
	}

	for i := range r.objects {
		r.objects[i].update()
	}

	for i := range r.effects {
		r.effects[i].Update()
	}

	_enemies := r.enemies[:0]
	for i := range r.enemies {
		if r.enemies[i].pos.Y < screenHeight+100 {
			_enemies = append(_enemies, r.enemies[i])
		}
	}
	r.enemies = _enemies

	_objects := r.objects[:0]
	for i := range r.objects {
		pos := mathutil.ConvertCoordinateWorldToScreen(r.objects[i].pos, cameraY, screenZ, screenWidth, screenHeight)
		if pos.X > -100 {
			_objects = append(_objects, r.objects[i])
		}
	}
	r.objects = _objects

	_effects := r.effects[:0]
	for i := range r.effects {
		if !r.effects[i].Finished() {
			_effects = append(_effects, r.effects[i])
		}
	}
	r.effects = _effects

	sort.Slice(r.objects, func(i, j int) bool {
		return r.objects[i].pos.Z > r.objects[j].pos.Z
	})
}

func (r *GameRunner) drawBackground(dst *ebiten.Image) {
	size := backgroundImg.Bounds().Size()
	for x := 0; x < screenWidth; x += size.X {
		opts := &ebiten.DrawImageOptions{}
		opts.GeoM.Translate(float64(x), 0)
		dst.DrawImage(backgroundImg, opts)
	}
}

func (r *GameRunner) drawRhino(dst *ebiten.Image) {
	if r.ticksAtHit > 0 && r.ticks-r.ticksAtHit < 10 {
		size := rhinoHitImg.Bounds().Size()
		x := rhinoX - float64(size.X)/2
		y := rhinoY - float64(size.Y)
		opts := &ebiten.DrawImageOptions{}
		opts.GeoM.Translate(x, y)
		dst.DrawImage(rhinoHitImg, opts)
	} else {
		p := 10
		if r.rush {
			p /= 2
		}
		periodicOffset := int(r.ticks / uint64(p) % 2)
		stateOffset := 0
		if r.gage > 0 {
			stateOffset += 2
		}
		index := periodicOffset + stateOffset
		img := rhinoImg[index]
		size := img.Bounds().Size()
		x := rhinoX - float64(size.X)/2
		y := rhinoY - float64(size.Y) - float64(periodicOffset*5)
		opts := &ebiten.DrawImageOptions{}
		opts.GeoM.Translate(x, y)
		dst.DrawImage(img, opts)
	}
}

func (r *GameRunner) drawGage(dst *ebiten.Image) {
	var path vector.Path

	const radius = 80.0

	imgSize := rhinoImg[0].Bounds().Size()
	x, y := float32(rhinoX), float32(rhinoY)-float32(imgSize.Y)/2
	path.MoveTo(x, y-radius)
	path.Arc(x, y, float32(radius), -math.Pi/2, float32(-math.Pi/2+2*math.Pi*float32(r.gage)/gageMax), vector.Clockwise)

	op := &vector.StrokeOptions{}
	op.Width = 5
	op.LineJoin = vector.LineJoinRound
	vs, is := path.AppendVerticesAndIndicesForStroke(nil, nil, op)

	for i := range vs {
		vs[i].SrcX = 1
		vs[i].SrcY = 1
		vs[i].ColorR = float32(0xfa) / 0xff
		vs[i].ColorG = float32(0xe8) / 0xff
		vs[i].ColorB = float32(0xe8) / 0xff
		vs[i].ColorA = 1.0
	}

	opts := &ebiten.DrawTrianglesOptions{}
	dst.DrawTriangles(vs, is, emptySubImage, opts)
}

func (r *GameRunner) draw(dst *ebiten.Image) {
	r.drawBackground(dst)

	for i := 0; i < len(r.objects); i++ {
		if r.objects[i].pos.Z > rhinoZ {
			r.objects[i].draw((dst))
		}
	}

	r.drawRhino(dst)

	r.drawGage(dst)

	for i := range r.enemies {
		r.enemies[i].draw(dst)
	}

	for i := range r.objects {
		if r.objects[i].pos.Z <= rhinoZ {
			r.objects[i].draw((dst))
		}
	}

	for i := range r.effects {
		r.effects[i].Draw(dst)
	}
}

type GameMode int

const (
	GameModeTitle GameMode = iota
	GameModePlaying
	GameModeGameOver
)

type Game struct {
	playerID        string
	sessionID       string
	playID          string
	random          *rand.Rand
	mode            GameMode
	modeTicks       uint64
	touches         []touchutil.Touch
	touchSimulation *touchutil.TouchSimulation
	runner          *GameRunner
	highScore       int
	debug           bool
}

func (g *Game) Update() error {
	g.modeTicks++

	g.touches = touchutil.UpdateTouches(g.touches)

	loggingutil.SendTouchLog(gameName, g.playerID, g.sessionID, g.playID, g.modeTicks, g.touches)

	switch g.mode {
	case GameModeTitle:
		touches := g.touchSimulation.Next()
		g.runner.update(touches)
		if g.runner.gameOver {
			g.runner = g.newGameRunner(true)
			g.touchSimulation = g.generateTouchSimulation()
		}

		if touchutil.AnyTouchesJustTouched(g.touches) {
			loggingutil.SendLog(gameName, g.playerID, g.sessionID, g.playID, &loggingutil.StartGamePayload{})

			g.runner = g.newGameRunner(false)

			g.setNextMode(GameModePlaying)

			audioContext.NewPlayerFromBytes(gameStartAudioData).Play()
		}
	case GameModePlaying:
		g.runner.update(g.touches)

		if g.runner.score > g.highScore {
			g.highScore = g.runner.score
		}

		if g.runner.gameOver {
			loggingutil.SendLog(gameName, g.playerID, g.sessionID, g.playID, &loggingutil.GameOverPayload{Score: g.runner.score})

			g.setNextMode(GameModeGameOver)

			audioContext.NewPlayerFromBytes(gameOverAudioData).Play()
		}
	case GameModeGameOver:
		if g.modeTicks > 60 && touchutil.AnyTouchesJustTouched(g.touches) {
			g.initialize()
		}
	}

	return nil
}

func (g *Game) drawScore(dst *ebiten.Image) {
	resourceutilv2.DrawTextWithFace(dst, fmt.Sprintf("SCORE %d HI %d", g.runner.score, g.highScore),
		screenWidth-10, 10, text.AlignEnd, color.White, fontFaceS, 0)
}

func (g *Game) drawTitle(dst *ebiten.Image) {
	resourceutilv2.DrawTextWithFace(dst, "RHINOCEROS",
		screenWidth/2, 120, text.AlignCenter, color.RGBA{0, 0, 0x50, 0xff}, fontFaceL, 0)

	resourceutilv2.DrawTextWithFace(dst, "[HOLD] Charge\n[RELEASE] Rush",
		screenWidth/2, 260, text.AlignCenter, color.RGBA{0, 0, 0x50, 0xff}, fontFaceS, 2.4)

	resourceutilv2.DrawTextWithFace(dst, "CREATOR: NAOKI TSUJIO\nFONT: Press Start 2P by CodeMan38\nSOUND EFFECT: MaouDamashii",
		screenWidth/2, 410, text.AlignCenter, color.RGBA{0, 0, 0x50, 0xff}, fontFaceS, 1.8)
}

func (g *Game) drawGameOver(dst *ebiten.Image) {
	resourceutilv2.DrawTextWithFace(dst, "GAME OVER",
		screenWidth/2, 175, text.AlignCenter, color.RGBA{0, 0, 0x50, 0xff}, fontFaceL, 0)

	resourceutilv2.DrawTextWithFace(dst, fmt.Sprintf("YOUR SCORE IS\n%d!", g.runner.score),
		screenWidth/2, 260, text.AlignCenter, color.RGBA{0, 0, 0x50, 0xff}, fontFaceM, 1.8)
}

func (g *Game) Draw(screen *ebiten.Image) {
	switch g.mode {
	case GameModeTitle:
		g.runner.draw(screen)
		g.drawTitle(screen)
	case GameModePlaying:
		g.runner.draw(screen)
		g.drawScore(screen)
	case GameModeGameOver:
		g.runner.draw(screen)
		g.drawScore(screen)
		g.drawGameOver(screen)
	}

	if g.debug {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("%.1f", ebiten.ActualTPS()))
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func (g *Game) setNextMode(mode GameMode) {
	g.mode = mode
	g.modeTicks = 0
}

func (g *Game) generateTouchSimulation() *touchutil.TouchSimulation {
	s := touchutil.NewSimulation()
	for i := 0; i < 3; i++ {
		s = s.Wait(30 + g.random.Int()%60).
			Touch().
			Wait(30 + g.random.Int()%30).
			Release()
	}
	return s
}

func (g *Game) newGameRunner(mute bool) *GameRunner {
	runner := &GameRunner{game: g, mute: mute}

	for i := 0; i < 99; i++ {
		x := g.random.Float64() * screenWidth
		o := runner.createBackgroundObject("tree", x)
		runner.objects = append(runner.objects, *o)
	}

	for i := 0; i < 99; i++ {
		x := g.random.Float64() * screenWidth
		o := runner.createBackgroundObject("weed", x)
		runner.objects = append(runner.objects, *o)
	}

	for i := 0; i < 20; i++ {
		x := g.random.Float64() * screenWidth
		o := runner.createBackgroundObject("cloud", x)
		runner.objects = append(runner.objects, *o)
	}

	return runner
}

func (g *Game) initialize() {
	if playIDObj, err := uuid.NewRandom(); err == nil {
		g.playID = playIDObj.String()
	} else {
		g.playID = ""
	}

	seed := time.Now().Unix()

	loggingutil.SendLog(gameName, g.playerID, g.sessionID, g.playID, &loggingutil.InitializePayload{RandomSeed: seed})

	g.random = rand.New(rand.NewPCG(uint64(seed), uint64(seed)))
	g.touches = nil
	g.touchSimulation = g.generateTouchSimulation()
	g.runner = g.newGameRunner(true)

	g.setNextMode(GameModeTitle)
}

func main() {
	if secret, err := resources.ReadFile("resources/secret"); err == nil && os.Getenv("GAME_LOGGING") == "1" {
		logging.Enable(string(secret))
	} else {
		logging.Disable()
	}

	playerID := os.Getenv("GAME_PLAYER_ID")

	sessionID := ""
	if sessionIDObj, err := uuid.NewRandom(); err == nil {
		sessionID = sessionIDObj.String()
	}

	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Rhinoceros")

	game := &Game{
		playerID:  playerID,
		sessionID: sessionID,
		debug:     os.Getenv("GAME_DEBUG") == "1",
	}
	game.initialize()

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
