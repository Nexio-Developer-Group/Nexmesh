import logo from '@/assets/logo.png'

/** Isometric N mark plus the "Nex" + gradient "mesh" wordmark. */
export function Logo({ size = 22 }: { size?: number }) {
  return (
    <span className="inline-flex items-center gap-2 font-extrabold tracking-tight" style={{ fontSize: size * 0.82 }}>
      <img src={logo} alt="" width={size} height={size * 0.85} style={{ height: size }} className="object-contain" />
      <span aria-label="Nexmesh">
        <span className="text-fg">Nex</span>
        <span className="bg-nx-gradient bg-clip-text text-transparent">mesh</span>
      </span>
    </span>
  )
}
