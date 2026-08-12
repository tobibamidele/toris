import { useEffect, useRef } from 'react'
import Globe, { type GlobeInstance } from 'globe.gl'
import { useTheme } from '../lib/theme'

interface ClusterNode {
  id: string
  name: string
  lat: number
  lng: number
  role: 'primary' | 'replica'
}

const NODES: ClusterNode[] = [
  { id: 'node-01', name: 'Lagos', lat: 6.4541, lng: 3.3947, role: 'primary'}, 
  { id: 'node-02', name: 'New York', lat: 40.7128, lng: -74.006, role: 'replica' },
  { id: 'node-03', name: 'San Francisco', lat: 37.7749, lng: -122.4194, role: 'replica' },
  { id: 'node-04', name: 'Toronto', lat: 43.6532, lng: -79.3832, role: 'replica' },
  { id: 'node-05', name: 'São Paulo', lat: -23.5505, lng: -46.6333, role: 'replica' },
  { id: 'node-06', name: 'London', lat: 51.5074, lng: -0.1278, role: 'replica' },
  { id: 'node-07', name: 'Frankfurt', lat: 50.1109, lng: 8.6821, role: 'replica' },
  { id: 'node-08', name: 'Johannesburg', lat: -26.2041, lng: 28.0473, role: 'replica' },
  { id: 'node-09', name: 'Mumbai', lat: 19.076, lng: 72.8777, role: 'replica' },
  { id: 'node-10', name: 'Singapore', lat: 1.3521, lng: 103.8198, role: 'replica' },
  { id: 'node-11', name: 'Tokyo', lat: 35.6762, lng: 139.6503, role: 'replica' },
  { id: 'node-12', name: 'Sydney', lat: -33.8688, lng: 151.2093, role: 'replica' },
]

const PRIMARY = NODES.find((n) => n.role === 'primary')!

const REPLICATION_ARCS = NODES.filter((n) => n.role === 'replica').map(
  (n, i) => ({
    i,
    startLat: PRIMARY.lat,
    startLng: PRIMARY.lng,
    endLat: n.lat,
    endLng: n.lng,
  }),
)

export function ClusterGlobe({ className }: { className?: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const globeRef = useRef<GlobeInstance | null>(null)
  const { theme } = useTheme()
  const dark = theme === 'dark'

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const reducedMotion = window.matchMedia(
      '(prefers-reduced-motion: reduce)',
    ).matches

    const globe = new Globe(el, { animateIn: true })
      .backgroundColor('rgba(0,0,0,0)')
      .showGraticules(false)
      .showAtmosphere(true)
      .atmosphereAltitude(0.14)
      .pointOfView({ lat: 0, lng: 30, altitude: 2.4 }, 0)

    globeRef.current = globe

    const controls = globe.controls()
    controls.autoRotate = !reducedMotion
    controls.autoRotateSpeed = 0.55
    controls.enableZoom = false
    controls.enablePan = false
    controls.rotateSpeed = 0.7

    const onEnter = () => {
      if (!reducedMotion) controls.autoRotate = false
    }
    const onLeave = () => {
      if (!reducedMotion) controls.autoRotate = true
    }
    el.addEventListener('pointerenter', onEnter)
    el.addEventListener('pointerleave', onLeave)

    const resize = () => {
      globe.width(el.clientWidth).height(el.clientHeight)
    }
    const ro = new ResizeObserver(resize)
    ro.observe(el)
    resize()

    return () => {
      el.removeEventListener('pointerenter', onEnter)
      el.removeEventListener('pointerleave', onLeave)
      ro.disconnect()
      globe._destructor()
      globeRef.current = null
    }
  }, [])

  useEffect(() => {
    const globe = globeRef.current
    if (!globe) return
    const reducedMotion = window.matchMedia(
      '(prefers-reduced-motion: reduce)',
    ).matches

    const primaryColor = dark ? '#ffffff' : '#000000'
    const replicaColor = dark
      ? 'rgba(255, 255, 255, 0.5)'
      : 'rgba(0, 0, 0, 0.42)'
    const arcColor = dark ? 'rgba(255, 255, 255, 0.72)' : 'rgba(0, 0, 0, 0.68)'
    const labelColor = dark ? 'rgba(255,255,255,0.92)' : 'rgba(0,0,0,0.92)'

    globe
      .globeImageUrl(
        dark ? '/textures/earth-dark.jpg' : '/textures/earth-light.jpg',
      )
      .bumpImageUrl('/textures/earth-topology.png')
      .atmosphereColor(dark ? '#ffffff' : '#000000')

    globe
      .pointsData(NODES)
      .pointLat((d) => (d as ClusterNode).lat)
      .pointLng((d) => (d as ClusterNode).lng)
      .pointAltitude((d) =>
        (d as ClusterNode).role === 'primary' ? 0.07 : 0.035,
      )
      .pointRadius((d) =>
        (d as ClusterNode).role === 'primary' ? 0.62 : 0.34,
      )
      .pointResolution(24)
      .pointColor((d) =>
        (d as ClusterNode).role === 'primary' ? primaryColor : replicaColor,
      )

    globe
      .arcsData(REPLICATION_ARCS)
      .arcStartLat('startLat')
      .arcStartLng('startLng')
      .arcEndLat('endLat')
      .arcEndLng('endLng')
      .arcColor(() => arcColor)
      .arcAltitudeAutoScale(0.55)
      .arcStroke(0.5)
      .arcDashLength(0.55)
      .arcDashGap(1.4)
      .arcDashInitialGap((d) => (d as { i: number }).i * 0.42)
      .arcDashAnimateTime(2400)

    globe
      .ringsData(reducedMotion ? [] : [{ lat: PRIMARY.lat, lng: PRIMARY.lng }])
      .ringLat((d) => (d as { lat: number }).lat)
      .ringLng((d) => (d as { lng: number }).lng)
      .ringAltitude(0.09)
      .ringMaxRadius(2.2)
      .ringPropagationSpeed(1.1)
      .ringRepeatPeriod(2400)
      .ringResolution(128)
      .ringColor(() => primaryColor)

    globe
      .labelsData([PRIMARY])
      .labelLat((d) => (d as ClusterNode).lat)
      .labelLng((d) => (d as ClusterNode).lng)
      .labelAltitude(0.24)
      .labelText(() => 'PRIMARY')
      .labelColor(() => labelColor)
      .labelDotRadius(0)
      .labelSize(1.15)
  }, [dark])

  return (
    <div ref={containerRef} className={className} aria-hidden="true" />
  )
}
