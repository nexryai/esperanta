# COPR builds this spec through the SCM "make srpm" method
# (see .copr/Makefile).
Name:           esperanta
Version:        0.1.2
Release:        1%{?dist}
Summary:        IP-restricted HTTP and HTTPS forward proxy

License:        MIT
URL:            https://github.com/nexryai/esperanta
Source0:        %{url}/archive/refs/tags/v%{version}.tar.gz

BuildRequires:  golang >= 1.21
BuildRequires:  ca-certificates
Requires:       systemd
Requires:       ca-certificates

%global debug_package %{nil}

%description
Esperanta is a simple forward proxy with YAML configuration, connection
pooling, bounded concurrency and HTTPS CONNECT support. The daemon runs as a
DynamicUser and applies a read-only Landlock sandbox.

%prep
%autosetup -n %{name}-%{version}

%build
export CGO_ENABLED=0
export GOFLAGS=-mod=mod
export MS_GOTOOLCHAIN_ALLOW_NON_LOCAL=1
export GOTOOLCHAIN=auto

%ifarch x86_64
%global goarch amd64
%endif
%ifarch aarch64
%global goarch arm64
%endif
%ifarch ppc64le
%global goarch ppc64le
%endif
%ifarch s390x
%global goarch s390x
%endif
%ifarch riscv64
%global goarch riscv64
%endif
%{?goarch:export GOARCH=%{goarch}}

go build -buildvcs=false -trimpath -ldflags "-s -w" -o esperanta ./cmd/esperanta

%check
export CGO_ENABLED=0
export GOFLAGS=-mod=mod
export MS_GOTOOLCHAIN_ALLOW_NON_LOCAL=1
export GOTOOLCHAIN=auto

# Kernel Landlock behavior is covered by CI; package builds may run on hosts
# whose kernel or sandbox environment cannot execute those tests reliably.
go test -skip '^TestKernelSandbox' ./...

%install
install -Dpm0755 esperanta %{buildroot}%{_bindir}/esperanta
install -Dpm0644 systemd/esperanta.service %{buildroot}%{_prefix}/lib/systemd/system/esperanta.service
install -Dpm0644 config.yaml %{buildroot}%{_sysconfdir}/esperanta/config.yaml

%post
systemctl daemon-reload >/dev/null 2>&1 || :

%preun
if [ $1 -eq 0 ]; then
    systemctl stop esperanta.service >/dev/null 2>&1 || :
    systemctl disable esperanta.service >/dev/null 2>&1 || :
fi

%postun
systemctl daemon-reload >/dev/null 2>&1 || :
if [ $1 -eq 1 ]; then
    systemctl try-restart esperanta.service >/dev/null 2>&1 || :
fi

%files
%license LICENSE
%doc README.md
%{_bindir}/esperanta
%{_prefix}/lib/systemd/system/esperanta.service
%config(noreplace) %{_sysconfdir}/esperanta/config.yaml

%changelog
* Mon Nov 23 2026 nexryai <nexryai@users.noreply.github.com> - 0.1.2-1
- Publish signed APT and pacman repositories on GitHub Pages
