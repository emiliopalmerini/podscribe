{
  description = "A small CLI for transcribing podcast audio with ElevenLabs";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    {
      self,
      nixpkgs,
    }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          version = self.shortRev or self.dirtyShortRev or "dirty";
          podscribe = pkgs.buildGoModule {
            pname = "podscribe";
            inherit version;

            src = ./.;
            vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";

            subPackages = [ "cmd/podscribe" ];
            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];

            meta = {
              description = "Transcribe podcast audio with the ElevenLabs Speech to Text API";
              homepage = "https://github.com/emiliopalmerini/podscribe";
              mainProgram = "podscribe";
            };
          };
        in
        {
          inherit podscribe;
          default = podscribe;
        }
      );

      apps = forAllSystems (
        system:
        let
          podscribe = self.packages.${system}.podscribe;
          app = {
            type = "app";
            program = "${podscribe}/bin/podscribe";
          };
        in
        {
          inherit app;
          podscribe = app;
          default = app;
        }
      );
    };
}
