<template>
  <div ref="containerRef" :style="{ position: 'relative', width: '550px', height: '400px' }">
    <div ref="purpleRef" :style="purpleStyle">
      <div ref="purpleFaceRef" :style="purpleFaceStyle">
        <div class="eyeball" data-max-distance="5" :style="smallEyeStyle">
          <div class="eyeball-pupil" :style="smallPupilStyle" />
        </div>
        <div class="eyeball" data-max-distance="5" :style="smallEyeStyle">
          <div class="eyeball-pupil" :style="smallPupilStyle" />
        </div>
      </div>
    </div>

    <div ref="blackRef" :style="blackStyle">
      <div ref="blackFaceRef" :style="blackFaceStyle">
        <div class="eyeball" data-max-distance="4" :style="blackEyeStyle">
          <div class="eyeball-pupil" :style="blackPupilStyle" />
        </div>
        <div class="eyeball" data-max-distance="4" :style="blackEyeStyle">
          <div class="eyeball-pupil" :style="blackPupilStyle" />
        </div>
      </div>
    </div>

    <div ref="orangeRef" :style="orangeStyle">
      <div ref="orangeFaceRef" :style="orangeFaceStyle">
        <div class="pupil" data-max-distance="5" :style="dotPupilStyle" />
        <div class="pupil" data-max-distance="5" :style="dotPupilStyle" />
      </div>
    </div>

    <div ref="yellowRef" :style="yellowStyle">
      <div ref="yellowFaceRef" :style="yellowFaceStyle">
        <div class="pupil" data-max-distance="5" :style="dotPupilStyle" />
        <div class="pupil" data-max-distance="5" :style="dotPupilStyle" />
      </div>
      <div ref="yellowMouthRef" :style="yellowMouthStyle" />
    </div>
  </div>
</template>

<script setup>
import gsap from 'gsap'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps({
  isTyping: { type: Boolean, default: false },
  showPassword: { type: Boolean, default: false },
  passwordLength: { type: Number, default: 0 },
  isPasswordGuardMode: { type: Boolean, default: false },
})

const containerRef = ref(null)
const mouseRef = ref({ x: 0, y: 0 })
const rafIdRef = ref(0)

const purpleRef = ref(null)
const blackRef = ref(null)
const yellowRef = ref(null)
const orangeRef = ref(null)

const purpleFaceRef = ref(null)
const blackFaceRef = ref(null)
const yellowFaceRef = ref(null)
const orangeFaceRef = ref(null)
const yellowMouthRef = ref(null)

const purpleBlinkTimerRef = ref()
const blackBlinkTimerRef = ref()
const purplePeekTimerRef = ref()
const lookingTimerRef = ref()

const isLookingRef = ref(false)
const isHidingPassword = computed(() => props.passwordLength > 0 && !props.showPassword)
const isShowingPassword = computed(() => props.passwordLength > 0 && props.showPassword)

const stateRef = ref({
  isTyping: props.isTyping,
  isHidingPassword: isHidingPassword.value,
  isShowingPassword: isShowingPassword.value,
  isLooking: false,
  isPasswordGuardMode: props.isPasswordGuardMode,
})

const quickToRef = ref(null)

const purpleStyle = {
  position: 'absolute', bottom: 0, left: '70px', width: '180px', height: '400px',
  backgroundColor: '#6C3FF5', borderRadius: '10px 10px 0 0', zIndex: 1,
  transformOrigin: 'bottom center', willChange: 'transform',
}

const blackStyle = {
  position: 'absolute', bottom: 0, left: '240px', width: '120px', height: '310px',
  backgroundColor: '#2D2D2D', borderRadius: '8px 8px 0 0', zIndex: 2,
  transformOrigin: 'bottom center', willChange: 'transform',
}

const orangeStyle = {
  position: 'absolute', bottom: 0, left: 0, width: '240px', height: '200px',
  backgroundColor: '#FF9B6B', borderRadius: '120px 120px 0 0', zIndex: 3,
  transformOrigin: 'bottom center', willChange: 'transform',
}

const yellowStyle = {
  position: 'absolute', bottom: 0, left: '310px', width: '140px', height: '230px',
  backgroundColor: '#E8D754', borderRadius: '70px 70px 0 0', zIndex: 4,
  transformOrigin: 'bottom center', willChange: 'transform',
}

const purpleFaceStyle = { position: 'absolute', display: 'flex', gap: '32px', left: '45px', top: '40px' }
const blackFaceStyle = { position: 'absolute', display: 'flex', gap: '24px', left: '26px', top: '32px' }
const orangeFaceStyle = { position: 'absolute', display: 'flex', gap: '32px', left: '82px', top: '90px' }
const yellowFaceStyle = { position: 'absolute', display: 'flex', gap: '24px', left: '52px', top: '40px' }
const yellowMouthStyle = {
  position: 'absolute', width: '80px', height: '4px', backgroundColor: '#2D2D2D',
  borderRadius: '9999px', left: '40px', top: '88px',
}

const eyeBase = {
  borderRadius: '50%', display: 'flex', alignItems: 'center', justifyContent: 'center',
  overflow: 'hidden', willChange: 'height',
}

const smallEyeStyle = { ...eyeBase, width: '18px', height: '18px', backgroundColor: 'white' }
const blackEyeStyle = { ...eyeBase, width: '16px', height: '16px', backgroundColor: 'white' }
const smallPupilStyle = { width: '7px', height: '7px', borderRadius: '50%', backgroundColor: '#2D2D2D', willChange: 'transform' }
const blackPupilStyle = { width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#2D2D2D', willChange: 'transform' }
const dotPupilStyle = { width: '12px', height: '12px', borderRadius: '50%', backgroundColor: '#2D2D2D', willChange: 'transform' }

const calcPos = (el) => {
  const rect = el.getBoundingClientRect()
  const cx = rect.left + rect.width / 2
  const cy = rect.top + rect.height / 3
  const dx = mouseRef.value.x - cx
  const dy = mouseRef.value.y - cy
  return {
    faceX: Math.max(-15, Math.min(15, dx / 20)),
    faceY: Math.max(-10, Math.min(10, dy / 30)),
    bodySkew: Math.max(-6, Math.min(6, -dx / 120)),
  }
}

const calcEyePos = (el, maxDist) => {
  const r = el.getBoundingClientRect()
  const cx = r.left + r.width / 2
  const cy = r.top + r.height / 2
  const dx = mouseRef.value.x - cx
  const dy = mouseRef.value.y - cy
  const dist = Math.min(Math.sqrt(dx ** 2 + dy ** 2), maxDist)
  const angle = Math.atan2(dy, dx)
  return { x: Math.cos(angle) * dist, y: Math.sin(angle) * dist }
}

const applyLookAtEachOther = () => {
  const qt = quickToRef.value
  if (qt) {
    qt.purpleFaceLeft(55)
    qt.purpleFaceTop(65)
    qt.blackFaceLeft(32)
    qt.blackFaceTop(12)
  }
  purpleRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: 3, y: 4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
  blackRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: 0, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
}

const applyHidingPassword = () => {
  const qt = quickToRef.value
  if (qt) {
    qt.purpleFaceLeft(55)
    qt.purpleFaceTop(65)
  }
}

const applyShowPassword = () => {
  const qt = quickToRef.value
  if (qt) {
    qt.purpleSkew(0); qt.blackSkew(0); qt.orangeSkew(0); qt.yellowSkew(0)
    qt.purpleX(0); qt.blackX(0)
    qt.purpleHeight(400)
    qt.purpleFaceLeft(20); qt.purpleFaceTop(35)
    qt.blackFaceLeft(10); qt.blackFaceTop(28)
    qt.orangeFaceX(50 - 82); qt.orangeFaceY(85 - 90)
    qt.yellowFaceX(20 - 52); qt.yellowFaceY(35 - 40)
    qt.mouthX(10 - 40); qt.mouthY(0)
  }
  purpleRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: -4, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
  blackRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: -4, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
  orangeRef.value?.querySelectorAll('.pupil').forEach((p) => {
    gsap.to(p, { x: -5, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
  yellowRef.value?.querySelectorAll('.pupil').forEach((p) => {
    gsap.to(p, { x: -5, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
  })
}

const applyPasswordGuardMode = () => {
  const qt = quickToRef.value
  if (qt) {
    qt.purpleSkew(0); qt.blackSkew(0); qt.orangeSkew(0); qt.yellowSkew(0)
    qt.purpleX(0); qt.blackX(0)
    qt.purpleHeight(400)
    qt.purpleFaceLeft(24); qt.purpleFaceTop(22)
    qt.blackFaceLeft(14); qt.blackFaceTop(20)
    qt.orangeFaceX(22 - 82); qt.orangeFaceY(72 - 90)
    qt.yellowFaceX(12 - 52); qt.yellowFaceY(22 - 40)
    qt.mouthX(-14); qt.mouthY(-8)
  }
  purpleRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: -5, y: -5, duration: 0.25, ease: 'power2.out', overwrite: 'auto' })
  })
  blackRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
    gsap.to(p, { x: -4, y: -4, duration: 0.25, ease: 'power2.out', overwrite: 'auto' })
  })
  orangeRef.value?.querySelectorAll('.pupil').forEach((p) => {
    gsap.to(p, { x: -5, y: -5, duration: 0.25, ease: 'power2.out', overwrite: 'auto' })
  })
  yellowRef.value?.querySelectorAll('.pupil').forEach((p) => {
    gsap.to(p, { x: -5, y: -5, duration: 0.25, ease: 'power2.out', overwrite: 'auto' })
  })
}

onMounted(() => {
  gsap.set('.pupil', { x: 0, y: 0 })
  gsap.set('.eyeball-pupil', { x: 0, y: 0 })

  if (!purpleRef.value || !blackRef.value || !orangeRef.value || !yellowRef.value ||
      !purpleFaceRef.value || !blackFaceRef.value || !orangeFaceRef.value ||
      !yellowFaceRef.value || !yellowMouthRef.value) {
    return
  }

  quickToRef.value = {
    purpleSkew: gsap.quickTo(purpleRef.value, 'skewX', { duration: 0.3, ease: 'power2.out' }),
    blackSkew: gsap.quickTo(blackRef.value, 'skewX', { duration: 0.3, ease: 'power2.out' }),
    orangeSkew: gsap.quickTo(orangeRef.value, 'skewX', { duration: 0.3, ease: 'power2.out' }),
    yellowSkew: gsap.quickTo(yellowRef.value, 'skewX', { duration: 0.3, ease: 'power2.out' }),
    purpleX: gsap.quickTo(purpleRef.value, 'x', { duration: 0.3, ease: 'power2.out' }),
    blackX: gsap.quickTo(blackRef.value, 'x', { duration: 0.3, ease: 'power2.out' }),
    purpleHeight: gsap.quickTo(purpleRef.value, 'height', { duration: 0.3, ease: 'power2.out' }),
    purpleFaceLeft: gsap.quickTo(purpleFaceRef.value, 'left', { duration: 0.3, ease: 'power2.out' }),
    purpleFaceTop: gsap.quickTo(purpleFaceRef.value, 'top', { duration: 0.3, ease: 'power2.out' }),
    blackFaceLeft: gsap.quickTo(blackFaceRef.value, 'left', { duration: 0.3, ease: 'power2.out' }),
    blackFaceTop: gsap.quickTo(blackFaceRef.value, 'top', { duration: 0.3, ease: 'power2.out' }),
    orangeFaceX: gsap.quickTo(orangeFaceRef.value, 'x', { duration: 0.2, ease: 'power2.out' }),
    orangeFaceY: gsap.quickTo(orangeFaceRef.value, 'y', { duration: 0.2, ease: 'power2.out' }),
    yellowFaceX: gsap.quickTo(yellowFaceRef.value, 'x', { duration: 0.2, ease: 'power2.out' }),
    yellowFaceY: gsap.quickTo(yellowFaceRef.value, 'y', { duration: 0.2, ease: 'power2.out' }),
    mouthX: gsap.quickTo(yellowMouthRef.value, 'x', { duration: 0.2, ease: 'power2.out' }),
    mouthY: gsap.quickTo(yellowMouthRef.value, 'y', { duration: 0.2, ease: 'power2.out' }),
  }

  const tick = () => {
    const container = containerRef.value
    const qt = quickToRef.value
    if (!container || !qt) return

    const { isTyping, isHidingPassword, isShowingPassword, isLooking, isPasswordGuardMode } = stateRef.value

    if (isPasswordGuardMode) {
      applyPasswordGuardMode()
      rafIdRef.value = requestAnimationFrame(tick)
      return
    }

    if (purpleRef.value && !isShowingPassword) {
      const pp = calcPos(purpleRef.value)
      if (isTyping || isHidingPassword) {
        qt.purpleSkew(pp.bodySkew - 12)
        qt.purpleX(40)
        qt.purpleHeight(440)
      } else {
        qt.purpleSkew(pp.bodySkew)
        qt.purpleX(0)
        qt.purpleHeight(400)
      }
    }

    if (blackRef.value && !isShowingPassword) {
      const bp = calcPos(blackRef.value)
      if (isLooking) {
        qt.blackSkew(bp.bodySkew * 1.5 + 10)
        qt.blackX(20)
      } else if (isTyping || isHidingPassword) {
        qt.blackSkew(bp.bodySkew * 1.5)
        qt.blackX(0)
      } else {
        qt.blackSkew(bp.bodySkew)
        qt.blackX(0)
      }
    }

    if (orangeRef.value && !isShowingPassword) {
      const op = calcPos(orangeRef.value)
      qt.orangeSkew(op.bodySkew)
      qt.orangeFaceX(op.faceX)
      qt.orangeFaceY(op.faceY)
    }

    if (yellowRef.value && !isShowingPassword) {
      const yp = calcPos(yellowRef.value)
      qt.yellowSkew(yp.bodySkew)
      qt.yellowFaceX(yp.faceX)
      qt.yellowFaceY(yp.faceY)
      qt.mouthX(yp.faceX)
      qt.mouthY(yp.faceY)
    }

    if (purpleRef.value && !isShowingPassword && !isLooking) {
      const pp = calcPos(purpleRef.value)
      const purpleFaceX = pp.faceX >= 0 ? Math.min(25, pp.faceX * 1.5) : pp.faceX
      qt.purpleFaceLeft(45 + purpleFaceX)
      qt.purpleFaceTop(40 + pp.faceY)
    }

    if (blackRef.value && !isShowingPassword && !isLooking) {
      const bp = calcPos(blackRef.value)
      qt.blackFaceLeft(26 + bp.faceX)
      qt.blackFaceTop(32 + bp.faceY)
    }

    if (!isShowingPassword) {
      container.querySelectorAll('.pupil').forEach((p) => {
        const el = p
        const maxDist = Number(el.dataset.maxDistance) || 5
        const ePos = calcEyePos(el, maxDist)
        gsap.set(el, { x: ePos.x, y: ePos.y })
      })

      if (!isLooking) {
        container.querySelectorAll('.eyeball').forEach((eb) => {
          const el = eb
          const maxDist = Number(el.dataset.maxDistance) || 10
          const pupil = el.querySelector('.eyeball-pupil')
          if (!pupil) return
          const ePos = calcEyePos(el, maxDist)
          gsap.set(pupil, { x: ePos.x, y: ePos.y })
        })
      }
    }

    rafIdRef.value = requestAnimationFrame(tick)
  }

  const onMove = (e) => {
    mouseRef.value = { x: e.clientX, y: e.clientY }
  }

  window.addEventListener('mousemove', onMove, { passive: true })
  rafIdRef.value = requestAnimationFrame(tick)

  const purpleEyeballs = purpleRef.value.querySelectorAll('.eyeball')
  const blackEyeballs = blackRef.value.querySelectorAll('.eyeball')

  const schedulePurpleBlink = () => {
    purpleBlinkTimerRef.value = setTimeout(() => {
      purpleEyeballs.forEach((el) => {
        gsap.to(el, { height: 2, duration: 0.08, ease: 'power2.in' })
      })
      setTimeout(() => {
        purpleEyeballs.forEach((el) => {
          const size = Number(el.style.width.replace('px', '')) || 18
          gsap.to(el, { height: size, duration: 0.08, ease: 'power2.out' })
        })
        schedulePurpleBlink()
      }, 150)
    }, Math.random() * 4000 + 3000)
  }

  const scheduleBlackBlink = () => {
    blackBlinkTimerRef.value = setTimeout(() => {
      blackEyeballs.forEach((el) => {
        gsap.to(el, { height: 2, duration: 0.08, ease: 'power2.in' })
      })
      setTimeout(() => {
        blackEyeballs.forEach((el) => {
          const size = Number(el.style.width.replace('px', '')) || 16
          gsap.to(el, { height: size, duration: 0.08, ease: 'power2.out' })
        })
        scheduleBlackBlink()
      }, 150)
    }, Math.random() * 4000 + 3000)
  }

  schedulePurpleBlink()
  scheduleBlackBlink()

  onBeforeUnmount(() => {
    window.removeEventListener('mousemove', onMove)
    cancelAnimationFrame(rafIdRef.value)
  })
})

watch(
  () => [props.isTyping, isHidingPassword.value, isShowingPassword.value, isLookingRef.value, props.isPasswordGuardMode],
  () => {
    stateRef.value = {
      isTyping: props.isTyping,
      isHidingPassword: isHidingPassword.value,
      isShowingPassword: isShowingPassword.value,
      isLooking: isLookingRef.value,
      isPasswordGuardMode: props.isPasswordGuardMode,
    }
  },
  { immediate: true },
)

watch(
  () => [isShowingPassword.value, props.passwordLength],
  () => {
    if (props.isPasswordGuardMode || !isShowingPassword.value || props.passwordLength <= 0) {
      clearTimeout(purplePeekTimerRef.value)
      return
    }
    const purpleEyePupils = purpleRef.value?.querySelectorAll('.eyeball-pupil')
    if (!purpleEyePupils?.length) return

    const schedulePeek = () => {
      purplePeekTimerRef.value = setTimeout(() => {
        purpleEyePupils.forEach((p) => {
          gsap.to(p, { x: 4, y: 5, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
        })
        const qt = quickToRef.value
        if (qt) {
          qt.purpleFaceLeft(20)
          qt.purpleFaceTop(35)
        }
        setTimeout(() => {
          purpleEyePupils.forEach((p) => {
            gsap.to(p, { x: -4, y: -4, duration: 0.3, ease: 'power2.out', overwrite: 'auto' })
          })
          schedulePeek()
        }, 800)
      }, Math.random() * 3000 + 2000)
    }
    schedulePeek()
  },
  { immediate: true },
)

watch(
  () => [props.isTyping, isShowingPassword.value],
  () => {
    if (props.isPasswordGuardMode) {
      clearTimeout(lookingTimerRef.value)
      isLookingRef.value = false
      stateRef.value.isLooking = false
      return
    }
    if (props.isTyping && !isShowingPassword.value) {
      isLookingRef.value = true
      stateRef.value.isLooking = true
      applyLookAtEachOther()
      clearTimeout(lookingTimerRef.value)
      lookingTimerRef.value = setTimeout(() => {
        isLookingRef.value = false
        stateRef.value.isLooking = false
        purpleRef.value?.querySelectorAll('.eyeball-pupil').forEach((p) => {
          gsap.killTweensOf(p)
        })
      }, 800)
    } else {
      clearTimeout(lookingTimerRef.value)
      isLookingRef.value = false
      stateRef.value.isLooking = false
    }
  },
  { immediate: true },
)

watch(
  () => [isHidingPassword.value, isShowingPassword.value],
  () => {
    if (props.isPasswordGuardMode) {
      applyPasswordGuardMode()
    } else if (isShowingPassword.value) {
      applyShowPassword()
    } else if (isHidingPassword.value) {
      applyHidingPassword()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  clearTimeout(purpleBlinkTimerRef.value)
  clearTimeout(blackBlinkTimerRef.value)
  clearTimeout(purplePeekTimerRef.value)
  clearTimeout(lookingTimerRef.value)
  cancelAnimationFrame(rafIdRef.value)
})
</script>
