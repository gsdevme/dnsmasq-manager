# Changelog

## [0.2.0](https://github.com/gsdevme/dnsmasq-manager/compare/v0.1.0...v0.2.0) (2026-11-23)


### Features

* **agents:** add the project-manager agent ([73d3070](https://github.com/gsdevme/dnsmasq-manager/commit/73d3070739caef8f309abd8db092debafcb500b9))
* **api:** add the REST API with bearer auth ([e049c00](https://github.com/gsdevme/dnsmasq-manager/commit/e049c00d704906319fd295edff2351312af8befb))
* **cli:** add the serve command ([4880520](https://github.com/gsdevme/dnsmasq-manager/commit/4880520b577721d7b80eee5bd6f5daf481b6e4e3))
* **config:** load configuration from the environment ([32baa14](https://github.com/gsdevme/dnsmasq-manager/commit/32baa14d4781b4dc2dbf21a721d342464848c580))
* **health:** check the hosts directory and probe dnsmasq ([19b150f](https://github.com/gsdevme/dnsmasq-manager/commit/19b150f91100185fa0b45c106a7d74d1e50d13d4))
* **history:** add the request history ring ([1000351](https://github.com/gsdevme/dnsmasq-manager/commit/10003511b8e35d2def21f527c844e5210874566a))
* **hooks:** add pm-issue script for issue reads and updates ([0abda86](https://github.com/gsdevme/dnsmasq-manager/commit/0abda86003ace39fd6e4f18ff10ef2f1ef4dfc25))
* **hooks:** ask for an issue sync when a turn ends ([f1f335e](https://github.com/gsdevme/dnsmasq-manager/commit/f1f335e36145aed97fcb70fbc9a6fda6fa5845b2))
* **hooks:** inject the linked issue at the start of work ([0fccb5e](https://github.com/gsdevme/dnsmasq-manager/commit/0fccb5e2a62267caa43a9b3870ffae0b70a007a6))
* **hooks:** post review findings from the hook as one comment ([ea38144](https://github.com/gsdevme/dnsmasq-manager/commit/ea38144e62b2dd555266b2a22173f6b2c8e97794))
* **hooks:** review each pull request a session opens ([0e2f6f4](https://github.com/gsdevme/dnsmasq-manager/commit/0e2f6f4c51807dc2861889e5bb5f76106b2a2746))
* **hostsfile:** load and save the managed file atomically ([77a1727](https://github.com/gsdevme/dnsmasq-manager/commit/77a1727df2ba21d2a560a0fd6e97c42fb1aaae6e))
* **hostsfile:** render and parse the managed file ([18bde33](https://github.com/gsdevme/dnsmasq-manager/commit/18bde33789fca44536d5e3103288cb158b5f2145))
* **records:** add record types and validation policy ([8ecbf0c](https://github.com/gsdevme/dnsmasq-manager/commit/8ecbf0c561e0337923e2b2337859be236504ffc1))
* **records:** add the store that serialises writes ([b44be0a](https://github.com/gsdevme/dnsmasq-manager/commit/b44be0ace1ff80c40e0d716cdf1dec4cdf2b2a84))
* **server:** bind and serve the listeners ([f48146d](https://github.com/gsdevme/dnsmasq-manager/commit/f48146da524b16a8b2dd63dfcbdf4bf2d41afeb6))
* **web:** add the read-only status page ([6480ffc](https://github.com/gsdevme/dnsmasq-manager/commit/6480ffcc17a612971e835d196ec4707090576437))
* **webhook:** add the external-dns webhook provider ([133f5d1](https://github.com/gsdevme/dnsmasq-manager/commit/133f5d11abd9d0ed129fe16ea06b0bc244f29a48))

## 0.1.0 (2026-11-17)


### Features

* add cloud session-start hook ([fa8d9a4](https://github.com/gsdevme/dnsmasq-manager/commit/fa8d9a49d44f399223c276666d3d6727404bf484))
* add Go module skeleton with version command ([124231e](https://github.com/gsdevme/dnsmasq-manager/commit/124231ed518d11818a50c8e093cd7dc12fe54f1b))
* **cli:** move the command line onto cobra ([e36e482](https://github.com/gsdevme/dnsmasq-manager/commit/e36e48227c50a82ae013768fbb1c766a8b0d4d81))
* **install:** add installer for Alpine with stub-command tests ([6ba9546](https://github.com/gsdevme/dnsmasq-manager/commit/6ba954644f3d50ec51208dac449e66f8120caeed))
