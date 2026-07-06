// tech-icons.ts — die Technologie-Bibliothek der Service-Map: kuratierte
// simple-icons (monochrom, MIT) + Erkennungsregeln über den Container-Image-
// Namen. Monochrom ist Absicht: das Logo trägt IDENTITÄT, die Statusfarben
// bleiben der Status-Skala vorbehalten (RETICLE-Gesetz №1/№2).
//
// detectTech("redis:7-alpine")        → "redis"
// detectTech("ghcr.io/acme/api:v2")   → null (kein Match → Fallback-Glyph)
// getTechIcon(slug)                   → { title, path } (24×24 viewBox)

import {
  siRedis, siPostgresql, siMysql, siMariadb, siMongodb, siSqlite, siApachecassandra,
  siClickhouse, siInfluxdb, siApachecouchdb, siCockroachlabs, siSupabase,
  siPlanetscale, siSurrealdb, siDuckdb, siSnowflake, siTimescale, siDgraph,
  siElasticsearch, siOpensearch, siMeilisearch, siAlgolia, siRabbitmq, siApachekafka, siNatsdotio, siApachepulsar, siMqtt, siCelery,
  siPython, siNodedotjs, siGo, siRust, siOpenjdk, siKotlin, siScala, siRuby,
  siPhp, siDotnet, siSwift, siElixir, siErlang, siHaskell, siLua, siPerl,
  siCplusplus, siC, siZig, siDeno, siBun, siTypescript, siJavascript,
  siNginx, siApache, siCaddy, siTraefikproxy, siEnvoyproxy, siKong, siIstio, siLinkerd, siConsul, siEtcd, siApachetomcat,
  siUbuntu, siDebian, siAlpinelinux, siFedora, siCentos, siArchlinux, siLinux,
  siRedhat, siSuse, siGooglecloud, siDigitalocean,
  siHetzner, siOvh, siCloudflare, siFastly, siAkamai,
  siKubernetes, siDocker, siPodman, siHelm, siRancher, siTerraform, siAnsible, siPulumi, siVagrant, siPacker,
  siPrometheus, siGrafana, siOpentelemetry, siElastic, siKibana, siLogstash,
  siFluentd, siFluentbit, siDatadog, siNewrelic, siSentry, siPagerduty,
  siJaeger, siThanos, siVictoriametrics, siJenkins, siGitlab, siGithub, siGitea, siBitbucket, siCircleci, siTravisci,
  siArgo, siFlux, siTekton, siDrone, siSonar,
  siVault, siKeycloak, siAuth0, siOkta, siLetsencrypt, siOpenssl,
  siMinio, siCeph, siApachehadoop, siApachespark, siApacheflink, siApacheairflow,
  siApachesuperset, siMetabase, siLooker, siReact, siNextdotjs, siVuedotjs, siNuxt, siAngular, siSvelte, siAstro,
  siDjango, siFlask, siFastapi, siSpring, siLaravel, siSymfony, siRubyonrails,
  siExpress, siNestjs, siGraphql, siApollographql, siHasura, siPrisma, siStrapi,
  siWordpress, siDrupal, siJoomla, siGhost, siDiscourse, siMattermost,
  siRocketdotchat, siNextcloud, siOwncloud, siGitforwindows,
  siOpenvpn, siWireguard, siPihole,
  siHomeassistant, siZigbee2mqtt, siNodered, siEclipsemosquitto,
  siTemporal, siStripe, siMailgun,
  siOllama, siHuggingface, siPytorch, siTensorflow, siJupyter, siMlflow,
  siRay, siNvidia,
  type SimpleIcon,
} from 'simple-icons';

// Katalog: slug → Icon. ~180 Technologien, leicht erweiterbar.
const CATALOG: Record<string, SimpleIcon> = Object.fromEntries(
  [
    siRedis, siPostgresql, siMysql, siMariadb, siMongodb, siSqlite, siApachecassandra,
    siClickhouse, siInfluxdb, siApachecouchdb, siCockroachlabs, siSupabase,
    siPlanetscale, siSurrealdb, siDuckdb, siSnowflake, siTimescale, siDgraph,
    siElasticsearch, siOpensearch, siMeilisearch, siAlgolia, siRabbitmq, siApachekafka, siNatsdotio, siApachepulsar, siMqtt, siCelery,
    siPython, siNodedotjs, siGo, siRust, siOpenjdk, siKotlin, siScala, siRuby,
    siPhp, siDotnet, siSwift, siElixir, siErlang, siHaskell, siLua, siPerl,
    siCplusplus, siC, siZig, siDeno, siBun, siTypescript, siJavascript,
    siNginx, siApache, siCaddy, siTraefikproxy, siEnvoyproxy, siKong, siIstio, siLinkerd, siConsul, siEtcd, siApachetomcat,
    siUbuntu, siDebian, siAlpinelinux, siFedora, siCentos, siArchlinux, siLinux,
    siRedhat, siSuse, siGooglecloud, siDigitalocean,
    siHetzner, siOvh, siCloudflare, siFastly, siAkamai,
    siKubernetes, siDocker, siPodman, siHelm, siRancher, siTerraform, siAnsible, siPulumi, siVagrant, siPacker,
    siPrometheus, siGrafana, siOpentelemetry, siElastic, siKibana, siLogstash,
    siFluentd, siFluentbit, siDatadog, siNewrelic, siSentry, siPagerduty,
    siJaeger, siThanos, siVictoriametrics, siJenkins, siGitlab, siGithub, siGitea, siBitbucket, siCircleci, siTravisci,
    siArgo, siFlux, siTekton, siDrone, siSonar,
    siVault, siKeycloak, siAuth0, siOkta, siLetsencrypt, siOpenssl,
    siMinio, siCeph, siApachehadoop, siApachespark, siApacheflink, siApacheairflow,
    siApachesuperset, siMetabase, siLooker, siReact, siNextdotjs, siVuedotjs, siNuxt, siAngular, siSvelte, siAstro,
    siDjango, siFlask, siFastapi, siSpring, siLaravel, siSymfony, siRubyonrails,
    siExpress, siNestjs, siGraphql, siApollographql, siHasura, siPrisma, siStrapi,
    siWordpress, siDrupal, siJoomla, siGhost, siDiscourse, siMattermost,
    siRocketdotchat, siNextcloud, siOwncloud, siGitforwindows,
    siOpenvpn, siWireguard, siPihole,
    siHomeassistant, siZigbee2mqtt, siNodered, siEclipsemosquitto,
    siTemporal, siStripe, siMailgun,
    siOllama, siHuggingface, siPytorch, siTensorflow, siJupyter, siMlflow,
    siRay, siNvidia,
  ].map((i) => [i.slug, i]),
);

export type TechIcon = { slug: string; title: string; path: string };

export function getTechIcon(slug: string | null | undefined): TechIcon | null {
  if (!slug) return null;
  const i = CATALOG[slug];
  return i ? { slug: i.slug, title: i.title, path: i.path } : null;
}

export function listTechIcons(): TechIcon[] {
  return Object.values(CATALOG)
    .map((i) => ({ slug: i.slug, title: i.title, path: i.path }))
    .sort((a, b) => a.title.localeCompare(b.title));
}

// Erkennungsregeln: Muster im IMAGE-Namen (ohne Registry/Tag) → slug.
// Reihenfolge zählt — spezifisch vor generisch.
const RULES: [RegExp, string][] = [
  [/postgres/, 'postgresql'],
  [/pgbouncer|pgpool/, 'postgresql'],
  [/mysql/, 'mysql'],
  [/mariadb/, 'mariadb'],
  [/mongo/, 'mongodb'],
  [/redis|valkey/, 'redis'],
  [/clickhouse/, 'clickhouse'],
  [/cassandra/, 'apachecassandra'],
  [/cockroach/, 'cockroachlabs'],
  [/influxdb/, 'influxdb'],
  [/couchdb/, 'apachecouchdb'],
  [/elasticsearch/, 'elasticsearch'],
  [/opensearch/, 'opensearch'],
  [/meilisearch/, 'meilisearch'],
  [/rabbitmq/, 'rabbitmq'],
  [/kafka/, 'apachekafka'],
  [/nats/, 'natsdotio'],
  [/pulsar/, 'apachepulsar'],
  [/mosquitto/, 'eclipsemosquitto'],
  [/zookeeper/, 'apachekafka'],
  [/temporal/, 'temporal'],
  [/minio/, 'minio'],
  [/ceph/, 'ceph'],
  [/vault/, 'vault'],
  [/keycloak/, 'keycloak'],
  [/nginx/, 'nginx'],
  [/httpd|apache(?!kafka|pulsar|cassandra)/, 'apache'],
  [/caddy/, 'caddy'],
  [/traefik/, 'traefikproxy'],
  [/envoy/, 'envoyproxy'],
  [/kong/, 'kong'],
  [/istio/, 'istio'],
  [/linkerd/, 'linkerd'],
  [/consul/, 'consul'],
  [/etcd/, 'etcd'],
  [/tomcat/, 'apachetomcat'],
  [/prometheus/, 'prometheus'],
  [/grafana/, 'grafana'],
  [/otel|opentelemetry/, 'opentelemetry'],
  [/beyla/, 'grafana'],
  [/jaeger/, 'jaeger'],
  [/thanos/, 'thanos'],
  [/victoriametrics/, 'victoriametrics'],
  [/kibana/, 'kibana'],
  [/logstash/, 'logstash'],
  [/fluentd/, 'fluentd'],
  [/fluent-?bit/, 'fluentbit'],
  [/jenkins/, 'jenkins'],
  [/gitlab/, 'gitlab'],
  [/gitea/, 'gitea'],
  [/argocd|argo/, 'argo'],
  [/flux/, 'flux'],
  [/tekton/, 'tekton'],
  [/sonarqube/, 'sonar'],
  [/airflow/, 'apacheairflow'],
  [/spark/, 'apachespark'],
  [/flink/, 'apacheflink'],
  [/superset/, 'apachesuperset'],
  [/metabase/, 'metabase'],
  [/python/, 'python'],
  [/node(:|$)|nodejs/, 'nodedotjs'],
  [/golang|(^|\/)go:|[-_]go(:|$)/, 'go'],
  [/rust/, 'rust'],
  [/openjdk|(^|\/)java|eclipse-temurin|amazoncorretto/, 'openjdk'],
  [/ruby/, 'ruby'],
  [/(^|\/)php/, 'php'],
  [/dotnet|aspnet/, 'dotnet'],
  [/elixir/, 'elixir'],
  [/erlang/, 'erlang'],
  [/deno/, 'deno'],
  [/(^|\/)bun(:|$)/, 'bun'],
  [/wordpress/, 'wordpress'],
  [/drupal/, 'drupal'],
  [/ghost/, 'ghost'],
  [/discourse/, 'discourse'],
  [/mattermost/, 'mattermost'],
  [/rocket\.?chat/, 'rocketdotchat'],
  [/nextcloud/, 'nextcloud'],
  [/ubuntu/, 'ubuntu'],
  [/debian/, 'debian'],
  [/alpine/, 'alpinelinux'],
  [/fedora/, 'fedora'],
  [/centos/, 'centos'],
  [/busybox/, 'linux'],
  [/ollama/, 'ollama'],
  [/pytorch/, 'pytorch'],
  [/tensorflow/, 'tensorflow'],
  [/jupyter/, 'jupyter'],
  [/huggingface|text-generation-inference/, 'huggingface'],
  [/nvidia|cuda|triton/, 'nvidia'],
  [/kube-|kubernetes|k8s|coredns|kube-proxy|pause/, 'kubernetes'],
  [/rancher/, 'rancher'],
  [/helm/, 'helm'],
];

// detectTech: Registry + Tag abschneiden, dann Regeln in Reihenfolge.
export function detectTech(image: string | null | undefined): string | null {
  if (!image) return null;
  // "ghcr.io/org/name:tag@sha" → "org/name"
  const noTag = (image.split('@')[0] ?? '').split(':')[0]?.toLowerCase() ?? '';
  const parts = noTag.split('/');
  const candidate =
    parts.length > 1 && (parts[0] ?? '').includes('.') ? parts.slice(1).join('/') : noTag;
  for (const [re, slug] of RULES) {
    if (re.test(candidate)) return slug;
  }
  return null;
}
