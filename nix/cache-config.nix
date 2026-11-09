let
  publicCaches = (import ../flake.nix).nixConfig;
  listen = "127.0.0.1:37515";
  repository = "hellodword/nixos-config";
  publicKey = "nixos-config-1:NfExPInFgDi/deX689T1M10hEORDGSZYRUapJMRbWM8=";
  substituter = "http://${listen}/ghcr.io/${repository}";
  render =
    config:
    builtins.concatStringsSep "\n" (
      builtins.attrValues (
        builtins.mapAttrs (name: values: "${name} = ${builtins.concatStringsSep " " values}") config
      )
    )
    + "\n";
in
{
  inherit listen substituter;
  public = render publicCaches;
  ci = render {
    extra-substituters = publicCaches.extra-substituters ++ [ substituter ];
    extra-trusted-public-keys = publicCaches.extra-trusted-public-keys ++ [ publicKey ];
  };
}
