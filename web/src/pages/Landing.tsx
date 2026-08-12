import { Hero } from '../sections/Hero'
import { OneDsn } from '../sections/OneDsn'
import { Features } from '../sections/Features'
import { Failover } from '../sections/Failover'
import { Pipeline } from '../sections/Pipeline'
import { Quickstart } from '../sections/Quickstart'
import { Boundaries } from '../sections/Boundaries'
import { Cta } from '../sections/Cta'

export function Landing() {
  return (
    <>
      <Hero />
      <OneDsn />
      <Features />
      <Failover />
      <Pipeline />
      <Quickstart />
      <Boundaries />
      <Cta />
    </>
  )
}
