{
  description = "CLI tools for CBE / D2L / Brightspace";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, self }:
    let
      supportedSystems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.buildGoModule {
            pname = "schooltools";
            version = "0.2.0";

            src = self;
            vendorHash = "sha256-m2qPzdz6eF+xm8oXsK0dbc+z6sQ1wOOMc1rbBQLd/3s=";

            meta = {
              description = "Headless CLI for CBE's D2L/Brightspace LMS";
              homepage = "https://github.com/IamCoder18/schooltools";
              license = pkgs.lib.licenses.mit;
              mainProgram = "schooltools";
            };
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/schooltools";
        };
      });
    };
}
